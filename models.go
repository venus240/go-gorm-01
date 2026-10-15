package main

import "gorm.io/gorm"

const (
	OrderPending   = "Pending"
	OrderCompleted = "Completed"
	OrderCancelled = "Cancelled"
)

// gorm.Model provides ID, CreatedAt, UpdatedAt and DeletedAt (soft delete).
type Category struct {
	gorm.Model
	Name        string    `gorm:"size:150;not null"`
	Description string    `gorm:"size:500"`
	Products    []Product `gorm:"foreignKey:CategoryID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
}

type Product struct {
	gorm.Model
	Name       string  `gorm:"size:150;not null"`
	Price      float64 `gorm:"type:decimal(12,2);not null;check:chk_product_price,price >= 0"`
	Quantity   int     `gorm:"not null;check:chk_product_quantity,quantity >= 0"`
	CategoryID uint    `gorm:"not null;index"`
	Category   Category
	Orders     []Order `gorm:"foreignKey:ProductID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
}

type Customer struct {
	gorm.Model
	Name   string  `gorm:"size:150;not null"`
	Email  string  `gorm:"size:254;not null;uniqueIndex"`
	Phone  string  `gorm:"size:30;not null"`
	Orders []Order `gorm:"foreignKey:CustomerID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
}

// One order contains one product, as required by the assignment.
type Order struct {
	gorm.Model
	CustomerID uint `gorm:"not null;index"`
	Customer   Customer
	ProductID  uint `gorm:"not null;index"`
	Product    Product
	Quantity   int     `gorm:"not null;check:chk_order_quantity,quantity > 0"`
	TotalPrice float64 `gorm:"type:decimal(14,2);not null;check:chk_order_total,total_price >= 0"`
	Status     string  `gorm:"size:20;not null;index;check:chk_order_status,status IN ('Pending','Completed','Cancelled')"`
}
