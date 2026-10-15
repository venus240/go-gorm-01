package main

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func createCategoryDB(db *gorm.DB, category *Category) error {
	if category == nil {
		return fmt.Errorf("กรุณาระบุประเภทสินค้า")
	}
	category.Name = strings.TrimSpace(category.Name)
	category.Description = strings.TrimSpace(category.Description)
	if err := validateText("ชื่อประเภทสินค้า", category.Name, 150); err != nil {
		return err
	}
	if len([]rune(category.Description)) > 500 {
		return fmt.Errorf("รายละเอียดต้องไม่เกิน 500 ตัวอักษร")
	}
	return db.Omit(clause.Associations).Create(category).Error
}

func getAllCategoriesDB(db *gorm.DB) ([]Category, error) {
	var categories []Category
	err := db.Order("id ASC").Find(&categories).Error
	return categories, err
}

func deleteCategoryByIDDB(db *gorm.DB, id uint) error {
	if err := validateID(id); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var category Category
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&category, id).Error; err != nil {
			return err
		}
		var count int64
		// Include deleted products so they can still be restored later.
		if err := tx.Unscoped().Model(&Product{}).Where("category_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("ลบประเภทสินค้าไม่ได้: ยังมีสินค้า %d รายการ รวมสินค้าที่ถูก soft delete", count)
		}
		return tx.Delete(&category).Error
	})
}

func categoryMenu(db *gorm.DB, ui *console) error {
	return ui.menu("ดูแลหมวดหมู่สินค้า", []menuAction{
		{"แสดงหมวดหมู่ที่เปิดใช้งาน", func() error {
			categories, err := getAllCategoriesDB(db)
			if err != nil {
				return err
			}
			for _, category := range categories {
				fmt.Printf("หมวด #%d | %s | รายละเอียด: %s\n", category.ID, category.Name, category.Description)
			}
			fmt.Printf("แสดงผล %d หมวดหมู่\n", len(categories))
			return nil
		}},
		{"สร้างหมวดหมู่ใหม่", func() error {
			name, err := ui.text("ชื่อหมวดหมู่")
			if err != nil {
				return err
			}
			description, err := ui.text("คำอธิบายหมวดหมู่ (ไม่ระบุก็ได้)")
			if err != nil {
				return err
			}
			category := Category{Name: name, Description: description}
			if err := createCategoryDB(db, &category); err != nil {
				return err
			}
			fmt.Printf("บันทึกหมวดหมู่แล้ว รหัสหมวด: %d\n", category.ID)
			return nil
		}},
		{"นำหมวดหมู่ออกจากรายการ (Soft Delete)", func() error {
			id, err := ui.id("รหัสหมวดหมู่")
			if err != nil {
				return err
			}
			return deleteCategoryByIDDB(db, id)
		}},
	})
}
