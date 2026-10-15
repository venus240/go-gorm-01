package main

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func validateOrderStatus(status string) error {
	if status != OrderPending && status != OrderCompleted && status != OrderCancelled {
		return fmt.Errorf("สถานะต้องเป็น Pending, Completed หรือ Cancelled เท่านั้น")
	}
	return nil
}

func createOrderDB(db *gorm.DB, customerID, productID uint, quantity int) (*Order, error) {
	if err := validateID(customerID); err != nil {
		return nil, err
	}
	if err := validateID(productID); err != nil {
		return nil, err
	}
	if quantity <= 0 {
		return nil, fmt.Errorf("จำนวนที่สั่งต้องมากกว่า 0")
	}
	var order Order
	err := db.Transaction(func(tx *gorm.DB) error {
		var customer Customer
		// Lock the customer too, preventing a concurrent delete during order creation.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&customer, customerID).Error; err != nil {
			return fmt.Errorf("ไม่พบลูกค้า: %w", err)
		}
		var product Product
		// FOR UPDATE serializes purchases of the same product and prevents overselling.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&product, productID).Error; err != nil {
			return fmt.Errorf("ไม่พบสินค้า: %w", err)
		}
		if product.Quantity < quantity {
			return fmt.Errorf("สต็อกไม่พอ: เหลือ %d แต่สั่ง %d", product.Quantity, quantity)
		}
		total := roundMoney(product.Price * float64(quantity))
		if err := validateMoney(total, maxOrderTotal); err != nil {
			return err
		}
		order = Order{
			CustomerID: customerID, ProductID: productID, Quantity: quantity,
			TotalPrice: total, Status: OrderPending,
		}
		if err := tx.Model(&Product{}).Where("id = ?", productID).
			Update("quantity", gorm.Expr("quantity - ?", quantity)).Error; err != nil {
			return err
		}
		// If insertion fails, Transaction rolls the stock reduction back as well.
		return tx.Omit(clause.Associations).Create(&order).Error
	})
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func findOrdersDB(query *gorm.DB) ([]Order, error) {
	var orders []Order
	// Order history must include products that have since been soft deleted.
	includeDeleted := func(db *gorm.DB) *gorm.DB { return db.Unscoped() }
	err := query.Preload("Customer", includeDeleted).Preload("Product", includeDeleted).
		Order("created_at DESC, id DESC").Find(&orders).Error
	return orders, err
}

func getOrdersByCustomerDB(db *gorm.DB, customerID uint) ([]Order, error) {
	if err := validateID(customerID); err != nil {
		return nil, err
	}
	return findOrdersDB(db.Where("customer_id = ?", customerID))
}

func getOrdersByStatusDB(db *gorm.DB, status string) ([]Order, error) {
	if err := validateOrderStatus(status); err != nil {
		return nil, err
	}
	return findOrdersDB(db.Where("status = ?", status))
}

// The range includes the entire first and last calendar day.
func getOrdersByDateRangeDB(db *gorm.DB, start, end time.Time) ([]Order, error) {
	if start.IsZero() || end.IsZero() {
		return nil, fmt.Errorf("กรุณาระบุวันที่เริ่มต้นและสิ้นสุด")
	}
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	end = time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location())
	if start.After(end) {
		return nil, fmt.Errorf("วันที่เริ่มต้นต้องไม่อยู่หลังวันที่สิ้นสุด")
	}
	return findOrdersDB(db.Where("created_at >= ? AND created_at < ?", start, end.AddDate(0, 0, 1)))
}

func getOrdersWithDetailsDB(db *gorm.DB) ([]Order, error) {
	return findOrdersDB(db)
}

func updateOrderStatusDB(db *gorm.DB, id uint, status string) error {
	if err := validateID(id); err != nil {
		return err
	}
	if err := validateOrderStatus(status); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var order Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, id).Error; err != nil {
			return err
		}
		if order.Status == status {
			return nil
		}
		if order.Status == OrderCancelled || status == OrderCancelled {
			var product Product
			if err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).First(&product, order.ProductID).Error; err != nil {
				return err
			}
			delta := order.Quantity
			if order.Status == OrderCancelled {
				if product.DeletedAt.Valid {
					return fmt.Errorf("เปิดคำสั่งซื้อกลับไม่ได้: สินค้าถูก soft delete แล้ว")
				}
				if product.Quantity < order.Quantity {
					return fmt.Errorf("เปิดคำสั่งซื้อกลับไม่ได้: สต็อกไม่พอ")
				}
				delta = -order.Quantity
			}
			// Cancellation restores stock once; reopening reserves it again.
			if err := tx.Unscoped().Model(&Product{}).Where("id = ?", order.ProductID).
				Update("quantity", gorm.Expr("quantity + ?", delta)).Error; err != nil {
				return err
			}
		}
		return tx.Model(&Order{}).Where("id = ?", id).Update("status", status).Error
	})
}

func printOrders(orders []Order, err error) error {
	if err != nil {
		return err
	}
	for _, order := range orders {
		fmt.Printf("รายการ #%d | ผู้ซื้อ: %d %s | สินค้า: %d %s | จำนวน: %d ชิ้น | ยอดชำระ: %.2f บาท | สถานะ: %s | วันที่: %s\n",
			order.ID, order.CustomerID, order.Customer.Name, order.ProductID, order.Product.Name,
			order.Quantity, order.TotalPrice, order.Status, order.CreatedAt.In(appLocation).Format("2006-01-02 15:04:05"))
	}
	fmt.Printf("แสดงผลรายการสั่งซื้อ %d รายการ\n", len(orders))
	return nil
}

func orderMenu(db *gorm.DB, ui *console) error {
	return ui.menu("จัดการรายการสั่งซื้อ", []menuAction{
		{"เปิดประวัติการซื้อทั้งหมด", func() error { return printOrders(getOrdersWithDetailsDB(db)) }},
		{"บันทึกการสั่งซื้อใหม่", func() error {
			customerID, err := ui.id("รหัสลูกค้าผู้สั่งซื้อ")
			if err != nil {
				return err
			}
			productID, err := ui.id("รหัสสินค้าที่เลือกซื้อ")
			if err != nil {
				return err
			}
			quantity, err := ui.integer("จำนวนชิ้นที่ต้องการซื้อ")
			if err != nil {
				return err
			}
			order, err := createOrderDB(db, customerID, productID, quantity)
			if err != nil {
				return err
			}
			fmt.Printf("บันทึกการสั่งซื้อเลขที่ %d | ยอดชำระ: %.2f บาท | สถานะ: %s\n", order.ID, order.TotalPrice, order.Status)
			return nil
		}},
		{"เรียกดูประวัติของลูกค้า", func() error {
			id, err := ui.id("รหัสลูกค้า")
			if err != nil {
				return err
			}
			return printOrders(getOrdersByCustomerDB(db, id))
		}},
		{"เลือกดูรายการตามสถานะ", func() error {
			status, err := ui.text("สถานะที่ต้องการดู: Pending / Completed / Cancelled")
			if err != nil {
				return err
			}
			return printOrders(getOrdersByStatusDB(db, strings.TrimSpace(status)))
		}},
		{"ระบุวันที่เพื่อกรองรายการซื้อ (รวมวันแรกและวันสุดท้าย)", func() error {
			start, err := ui.date("ค้นหาตั้งแต่วันที่ YYYY-MM-DD (ปี ค.ศ.)")
			if err != nil {
				return err
			}
			end, err := ui.date("ค้นหาถึงวันที่ YYYY-MM-DD (ปี ค.ศ.)")
			if err != nil {
				return err
			}
			return printOrders(getOrdersByDateRangeDB(db, start, end))
		}},
		{"ปรับสถานะรายการสั่งซื้อ", func() error {
			id, err := ui.id("เลขที่รายการสั่งซื้อ")
			if err != nil {
				return err
			}
			status, err := ui.text("เปลี่ยนสถานะเป็น: Pending / Completed / Cancelled")
			if err != nil {
				return err
			}
			return updateOrderStatusDB(db, id, strings.TrimSpace(status))
		}},
	})
}
