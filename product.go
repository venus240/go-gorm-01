package main

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func validateProduct(product *Product) error {
	if product == nil {
		return fmt.Errorf("กรุณาระบุสินค้า")
	}
	product.Name = strings.TrimSpace(product.Name)
	if err := validateText("ชื่อสินค้า", product.Name, 150); err != nil {
		return err
	}
	if err := validateMoney(product.Price, maxProductPrice); err != nil {
		return err
	}
	if product.Quantity < 0 {
		return fmt.Errorf("สต็อกต้องไม่ติดลบ")
	}
	if err := validateID(product.CategoryID); err != nil {
		return err
	}
	product.Price = roundMoney(product.Price)
	return nil
}

func createProductDB(db *gorm.DB, product *Product) error {
	if err := validateProduct(product); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var category Category
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&category, product.CategoryID).Error; err != nil {
			return fmt.Errorf("ไม่พบประเภทสินค้า: %w", err)
		}
		return tx.Omit(clause.Associations).Create(product).Error
	})
}

func findProductsDB(query *gorm.DB) ([]Product, error) {
	var products []Product
	err := query.Preload("Category").Find(&products).Error
	return products, err
}

func getAllProductsDB(db *gorm.DB) ([]Product, error) {
	return findProductsDB(db.Order("id ASC"))
}

func getProductByIDDB(db *gorm.DB, id uint) (*Product, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	var product Product
	err := db.Preload("Category").First(&product, id).Error
	return &product, err
}

func getProductByNameDB(db *gorm.DB, name string) ([]Product, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("กรุณาระบุชื่อที่ต้องการค้นหา")
	}
	return findProductsDB(db.Where("name LIKE ?", "%"+name+"%").Order("id ASC"))
}

func getProductsByCategoryDB(db *gorm.DB, categoryID uint) ([]Product, error) {
	if err := validateID(categoryID); err != nil {
		return nil, err
	}
	return findProductsDB(db.Where("category_id = ?", categoryID).Order("id ASC"))
}

func validatePriceRange(min, max float64) error {
	if err := validateMoney(min, maxProductPrice); err != nil {
		return err
	}
	if err := validateMoney(max, maxProductPrice); err != nil {
		return err
	}
	if min > max {
		return fmt.Errorf("ราคาต่ำสุดต้องไม่มากกว่าราคาสูงสุด")
	}
	return nil
}

func getProductsByPriceRangeDB(db *gorm.DB, min, max float64) ([]Product, error) {
	if err := validatePriceRange(min, max); err != nil {
		return nil, err
	}
	return findProductsDB(db.Where("price BETWEEN ? AND ?", min, max).Order("price ASC, id ASC"))
}

func getProductsLowStockDB(db *gorm.DB, threshold int) ([]Product, error) {
	if threshold < 0 {
		return nil, fmt.Errorf("เกณฑ์สต็อกต้องไม่ติดลบ")
	}
	return findProductsDB(db.Where("quantity < ?", threshold).Order("quantity ASC, id ASC"))
}

func getProductsByCategoryAndPriceDB(db *gorm.DB, categoryID uint, min, max float64) ([]Product, error) {
	if err := validateID(categoryID); err != nil {
		return nil, err
	}
	if err := validatePriceRange(min, max); err != nil {
		return nil, err
	}
	return findProductsDB(db.Where("category_id = ? AND price BETWEEN ? AND ?", categoryID, min, max).Order("price ASC, id ASC"))
}

func getProductsSortedByPriceDB(db *gorm.DB, direction string) ([]Product, error) {
	direction = strings.ToUpper(strings.TrimSpace(direction))
	if direction != "ASC" && direction != "DESC" {
		return nil, fmt.Errorf("เรียงราคาได้เฉพาะ ASC หรือ DESC")
	}
	// Only the validated keyword is interpolated into ORDER BY.
	return findProductsDB(db.Order("price " + direction).Order("id ASC"))
}

func getTopExpensiveProductsDB(db *gorm.DB, limit int) ([]Product, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("จำนวนสินค้าต้องมากกว่า 0")
	}
	return findProductsDB(db.Order("price DESC, id ASC").Limit(limit))
}

func updateProductByIDDB(db *gorm.DB, id uint, updated Product) error {
	if err := validateID(id); err != nil {
		return err
	}
	if err := validateProduct(&updated); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var category Category
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&category, updated.CategoryID).Error; err != nil {
			return fmt.Errorf("ไม่พบประเภทสินค้า: %w", err)
		}
		// Use a map so zero prices and zero stock are saved too.
		result := tx.Model(&Product{}).Where("id = ?", id).Updates(map[string]interface{}{
			"name": updated.Name, "price": updated.Price,
			"quantity": updated.Quantity, "category_id": updated.CategoryID,
		})
		return requireAffectedRow(result)
	})
}

func updateProductPriceDB(db *gorm.DB, id uint, price float64) error {
	if err := validateID(id); err != nil {
		return err
	}
	if err := validateMoney(price, maxProductPrice); err != nil {
		return err
	}
	return requireAffectedRow(db.Model(&Product{}).Where("id = ?", id).Update("price", roundMoney(price)))
}

// delta is the stock adjustment: +5 adds five, -2 removes two.
func updateProductStockDB(db *gorm.DB, id uint, delta int) error {
	if err := validateID(id); err != nil {
		return err
	}
	// An atomic conditional UPDATE prevents concurrent requests from making stock negative.
	result := db.Model(&Product{}).Where("id = ? AND quantity + ? >= 0", id, delta).
		Update("quantity", gorm.Expr("quantity + ?", delta))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		if _, err := getProductByIDDB(db, id); err != nil {
			return err
		}
		return fmt.Errorf("สต็อกไม่พอสำหรับการลดจำนวนนี้")
	}
	return nil
}

func softDeleteProductDB(db *gorm.DB, id uint) error {
	if err := validateID(id); err != nil {
		return err
	}
	// Order creation also locks this row, so a deleted product cannot receive a new order.
	return requireAffectedRow(db.Delete(&Product{}, id))
}

func hardDeleteProductDB(db *gorm.DB, id uint) error {
	if err := validateID(id); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var product Product
		if err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).First(&product, id).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Unscoped().Model(&Order{}).Where("product_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("ลบสินค้าถาวรไม่ได้: มีประวัติคำสั่งซื้อ %d รายการ ใช้ soft delete แทนได้", count)
		}
		return tx.Unscoped().Delete(&product).Error
	})
}

func getDeletedProductsDB(db *gorm.DB) ([]Product, error) {
	return findProductsDB(db.Unscoped().Where("deleted_at IS NOT NULL").Order("id ASC"))
}

func restoreProductDB(db *gorm.DB, id uint) error {
	if err := validateID(id); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var product Product
		if err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("deleted_at IS NOT NULL").First(&product, id).Error; err != nil {
			return err
		}
		var category Category
		if err := tx.First(&category, product.CategoryID).Error; err != nil {
			return fmt.Errorf("กู้คืนไม่ได้: ไม่พบประเภทสินค้า: %w", err)
		}
		return requireAffectedRow(tx.Unscoped().Model(&Product{}).Where("id = ?", id).Update("deleted_at", nil))
	})
}

func printProducts(products []Product, err error) error {
	if err != nil {
		return err
	}
	for _, product := range products {
		fmt.Printf("สินค้า #%d | %s | ราคาขาย: %.2f บาท | คงเหลือ: %d ชิ้น | หมวด: %d %s\n",
			product.ID, product.Name, product.Price, product.Quantity, product.CategoryID, product.Category.Name)
	}
	fmt.Printf("แสดงผลสินค้า %d รายการ\n", len(products))
	return nil
}

func (ui *console) productInput() (Product, error) {
	var product Product
	var err error
	if product.Name, err = ui.text("ชื่อสินค้าที่จำหน่าย"); err != nil {
		return product, err
	}
	if product.Price, err = ui.money("ราคาขายต่อชิ้น"); err != nil {
		return product, err
	}
	if product.Quantity, err = ui.integer("จำนวนคงคลัง"); err != nil {
		return product, err
	}
	product.CategoryID, err = ui.id("รหัสหมวดหมู่ (ตรวจสอบได้จากตัวเลือก 1 ในหน้าหลัก)")
	return product, err
}

func productMenu(db *gorm.DB, ui *console) error {
	// All product queries in the assignment are available from this menu.
	return ui.menu("จัดการคลังสินค้า", []menuAction{
		{"เปิดรายการสินค้า", func() error { return printProducts(getAllProductsDB(db)) }},
		{"ค้นหาจากรหัสสินค้า", func() error {
			id, err := ui.id("รหัสสินค้า")
			if err != nil {
				return err
			}
			product, err := getProductByIDDB(db, id)
			if err != nil {
				return err
			}
			return printProducts([]Product{*product}, nil)
		}},
		{"ค้นหาสินค้าจากคำในชื่อ", func() error {
			name, err := ui.text("คำที่ปรากฏในชื่อสินค้า")
			if err != nil {
				return err
			}
			return printProducts(getProductByNameDB(db, name))
		}},
		{"เลือกสินค้าจากหมวดหมู่", func() error {
			id, err := ui.id("รหัสหมวดหมู่")
			if err != nil {
				return err
			}
			return printProducts(getProductsByCategoryDB(db, id))
		}},
		{"กำหนดช่วงราคาที่ต้องการ", func() error {
			min, max, err := ui.priceRange()
			if err != nil {
				return err
			}
			return printProducts(getProductsByPriceRangeDB(db, min, max))
		}},
		{"ตรวจสินค้าคงเหลือน้อย", func() error {
			threshold, err := ui.integer("แสดงสินค้าที่เหลือน้อยกว่ากี่ชิ้น")
			if err != nil {
				return err
			}
			return printProducts(getProductsLowStockDB(db, threshold))
		}},
		{"กรองหมวดหมู่พร้อมราคา", func() error {
			id, err := ui.id("รหัสหมวดหมู่")
			if err != nil {
				return err
			}
			min, max, err := ui.priceRange()
			if err != nil {
				return err
			}
			return printProducts(getProductsByCategoryAndPriceDB(db, id, min, max))
		}},
		{"จัดลำดับราคาสินค้า", func() error {
			direction, err := ui.text("พิมพ์ ASC เพื่อเรียงราคาขึ้น หรือ DESC เพื่อเรียงราคาลง")
			if err != nil {
				return err
			}
			return printProducts(getProductsSortedByPriceDB(db, direction))
		}},
		{"แสดงสินค้าแพงสุดตามจำนวน", func() error {
			limit, err := ui.integer("ต้องการแสดงกี่รายการ")
			if err != nil {
				return err
			}
			return printProducts(getTopExpensiveProductsDB(db, limit))
		}},
		{"ลงทะเบียนสินค้าใหม่", func() error {
			product, err := ui.productInput()
			if err != nil {
				return err
			}
			if err := createProductDB(db, &product); err != nil {
				return err
			}
			fmt.Printf("บันทึกสินค้าเข้าคลังแล้ว รหัสสินค้า: %d\n", product.ID)
			return nil
		}},
		{"ปรับรายละเอียดสินค้า (ระบุข้อมูลใหม่ทุกช่อง)", func() error {
			id, err := ui.id("รหัสสินค้า")
			if err != nil {
				return err
			}
			product, err := ui.productInput()
			if err != nil {
				return err
			}
			return updateProductByIDDB(db, id, product)
		}},
		{"ตั้งราคาขายใหม่", func() error {
			id, err := ui.id("รหัสสินค้า")
			if err != nil {
				return err
			}
			price, err := ui.money("ราคาขายที่ต้องการใช้")
			if err != nil {
				return err
			}
			return updateProductPriceDB(db, id, price)
		}},
		{"ปรับยอดสินค้าคงคลัง", func() error {
			id, err := ui.id("รหัสสินค้า")
			if err != nil {
				return err
			}
			delta, err := ui.integer("จำนวนรับเข้าเป็นบวก / จำนวนตัดออกเป็นลบ (เช่น 5 หรือ -2)")
			if err != nil {
				return err
			}
			return updateProductStockDB(db, id, delta)
		}},
		{"นำสินค้าออกจากรายการ (Soft Delete)", func() error {
			id, err := ui.id("รหัสสินค้า")
			if err != nil {
				return err
			}
			return softDeleteProductDB(db, id)
		}},
		{"ลบข้อมูลสินค้าออกถาวร", func() error {
			id, err := ui.id("รหัสสินค้า")
			if err != nil {
				return err
			}
			confirmation, err := ui.text("ยืนยันการลบข้อมูลนี้ถาวรด้วยคำว่า DELETE")
			if err != nil {
				return err
			}
			if confirmation != "DELETE" {
				return fmt.Errorf("ยกเลิกการลบถาวร")
			}
			return hardDeleteProductDB(db, id)
		}},
		{"เปิดรายการสินค้าที่ถูกซ่อน", func() error { return printProducts(getDeletedProductsDB(db)) }},
		{"นำสินค้ากลับมาจำหน่าย", func() error {
			id, err := ui.id("รหัสสินค้า")
			if err != nil {
				return err
			}
			return restoreProductDB(db, id)
		}},
	})
}
