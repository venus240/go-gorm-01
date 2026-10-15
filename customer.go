package main

import (
	"fmt"
	"net/mail"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func validateCustomer(customer *Customer) error {
	if customer == nil {
		return fmt.Errorf("กรุณาระบุลูกค้า")
	}
	customer.Name = strings.TrimSpace(customer.Name)
	customer.Email = strings.ToLower(strings.TrimSpace(customer.Email))
	customer.Phone = strings.TrimSpace(customer.Phone)
	if err := validateText("ชื่อลูกค้า", customer.Name, 150); err != nil {
		return err
	}
	address, err := mail.ParseAddress(customer.Email)
	if err != nil || address.Address != customer.Email || len(customer.Email) > 254 {
		return fmt.Errorf("รูปแบบอีเมลไม่ถูกต้อง")
	}
	return validateText("เบอร์โทรศัพท์", customer.Phone, 30)
}

func createCustomerDB(db *gorm.DB, customer *Customer) error {
	if err := validateCustomer(customer); err != nil {
		return err
	}
	return db.Omit(clause.Associations).Create(customer).Error
}

func getAllCustomersDB(db *gorm.DB) ([]Customer, error) {
	var customers []Customer
	err := db.Order("id ASC").Find(&customers).Error
	return customers, err
}

func getCustomerByEmailDB(db *gorm.DB, email string) (*Customer, error) {
	var customer Customer
	email = strings.ToLower(strings.TrimSpace(email))
	err := db.Where("email = ?", email).First(&customer).Error
	return &customer, err
}

func updateCustomerByIDDB(db *gorm.DB, id uint, updated Customer) error {
	if err := validateID(id); err != nil {
		return err
	}
	if err := validateCustomer(&updated); err != nil {
		return err
	}
	result := db.Model(&Customer{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name": updated.Name, "email": updated.Email, "phone": updated.Phone,
	})
	return requireAffectedRow(result)
}

func deleteCustomerByIDDB(db *gorm.DB, id uint) error {
	if err := validateID(id); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var customer Customer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&customer, id).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Unscoped().Model(&Order{}).Where("customer_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("ลบลูกค้าไม่ได้: มีประวัติคำสั่งซื้อ %d รายการ", count)
		}
		return tx.Delete(&customer).Error
	})
}

func (ui *console) customerInput() (Customer, error) {
	var customer Customer
	var err error
	if customer.Name, err = ui.text("ชื่อ-นามสกุลลูกค้า"); err != nil {
		return customer, err
	}
	if customer.Email, err = ui.text("อีเมลติดต่อ"); err != nil {
		return customer, err
	}
	customer.Phone, err = ui.text("หมายเลขโทรศัพท์")
	return customer, err
}

func printCustomers(customers []Customer, err error) error {
	if err != nil {
		return err
	}
	for _, customer := range customers {
		fmt.Printf("ลูกค้า #%d | ชื่อ: %s | อีเมล: %s | โทร: %s\n", customer.ID, customer.Name, customer.Email, customer.Phone)
	}
	fmt.Printf("แสดงผลลูกค้า %d คน\n", len(customers))
	return nil
}

func customerMenu(db *gorm.DB, ui *console) error {
	return ui.menu("ทะเบียนลูกค้า", []menuAction{
		{"เปิดรายชื่อลูกค้า", func() error { return printCustomers(getAllCustomersDB(db)) }},
		{"บันทึกลูกค้ารายใหม่", func() error {
			customer, err := ui.customerInput()
			if err != nil {
				return err
			}
			if err := createCustomerDB(db, &customer); err != nil {
				return err
			}
			return printCustomers([]Customer{customer}, nil)
		}},
		{"ค้นหาจากอีเมลที่ลงทะเบียน", func() error {
			email, err := ui.text("อีเมลที่ต้องการค้นหา")
			if err != nil {
				return err
			}
			customer, err := getCustomerByEmailDB(db, email)
			if err != nil {
				return err
			}
			return printCustomers([]Customer{*customer}, nil)
		}},
		{"ปรับข้อมูลการติดต่อ", func() error {
			id, err := ui.id("รหัสลูกค้า")
			if err != nil {
				return err
			}
			customer, err := ui.customerInput()
			if err != nil {
				return err
			}
			return updateCustomerByIDDB(db, id, customer)
		}},
		{"นำลูกค้าออกจากทะเบียน (Soft Delete)", func() error {
			id, err := ui.id("รหัสลูกค้า")
			if err != nil {
				return err
			}
			return deleteCustomerByIDDB(db, id)
		}},
	})
}
