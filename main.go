package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	maxProductPrice = 9999999999.99   // DECIMAL(12,2)
	maxOrderTotal   = 999999999999.99 // DECIMAL(14,2)
)

var appLocation = time.FixedZone("Asia/Bangkok", 7*60*60)

type dbConfig struct {
	Host, Port, User, Password, Name string
}

func envOrDefault(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func loadDBConfig() dbConfig {
	return dbConfig{
		Host:     "127.0.0.1",
		Port:     "3306",
		User:     "root",
		Password: "",
		Name:     "store_db",
	}
}

func connectDB(config dbConfig) (*gorm.DB, error) {
	if !regexp.MustCompile(`^[a-zA-Z0-9_]{1,64}$`).MatchString(config.Name) {
		return nil, fmt.Errorf("DB_NAME ต้องเป็นตัวอักษรอังกฤษ ตัวเลข หรือ _ ความยาว 1-64 ตัวอักษร")
	}
	port, err := strconv.Atoi(config.Port)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("DB_PORT ต้องอยู่ระหว่าง 1-65535")
	}
	dsn := mysqlDriver.NewConfig()
	dsn.User, dsn.Passwd = config.User, config.Password
	dsn.Net, dsn.Addr = "tcp", net.JoinHostPort(config.Host, config.Port)
	dsn.ParseTime, dsn.Loc = true, appLocation
	dsn.ClientFoundRows = true
	dsn.Params = map[string]string{"charset": "utf8mb4"}
	dsn.Timeout, dsn.ReadTimeout, dsn.WriteTimeout = 5*time.Second, 10*time.Second, 10*time.Second
	open := func() (*gorm.DB, error) {
		return gorm.Open(mysql.Open(dsn.FormatDSN()), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent), TranslateError: true,
			NowFunc: func() time.Time { return time.Now().In(appLocation) },
		})
	}
	// First connect without a database, allowing the first run to create store_db.
	server, err := open()
	if err != nil {
		return nil, fmt.Errorf("เชื่อมต่อ MySQL ไม่สำเร็จ: %w", err)
	}
	serverSQL, err := server.DB()
	if err != nil {
		return nil, err
	}
	// Name is validated above because database identifiers cannot use ? placeholders.
	err = server.Exec("CREATE DATABASE IF NOT EXISTS `" + config.Name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci").Error
	closeErr := serverSQL.Close()
	if err != nil {
		return nil, fmt.Errorf("สร้างฐานข้อมูลไม่สำเร็จ: %w", err)
	}
	if closeErr != nil {
		return nil, closeErr
	}

	dsn.DBName = config.Name
	db, err := open()
	if err != nil {
		return nil, fmt.Errorf("เปิดฐานข้อมูลไม่สำเร็จ: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ตรวจสอบการเชื่อมต่อไม่สำเร็จ: %w", err)
	}
	if err := db.Set("gorm:table_options", "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci").
		AutoMigrate(&Category{}, &Product{}, &Customer{}, &Order{}); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("สร้าง/ปรับปรุงตารางไม่สำเร็จ: %w", err)
	}
	return db, nil
}

func validateID(id uint) error {
	if id == 0 {
		return fmt.Errorf("ID ต้องมากกว่า 0")
	}
	return nil
}

func validateText(label, value string, maxLength int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("กรุณาระบุ%s", label)
	}
	if len([]rune(value)) > maxLength {
		return fmt.Errorf("%sต้องไม่เกิน %d ตัวอักษร", label, maxLength)
	}
	return nil
}

func validateMoney(value, max float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > max {
		return fmt.Errorf("จำนวนเงินต้องอยู่ระหว่าง 0 ถึง %.2f", max)
	}
	return nil
}

func roundMoney(value float64) float64 { return math.Round(value*100) / 100 }

func requireAffectedRow(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// Seed products, customers and orders only when they are empty and a category exists.
func seedSampleDataDB(db *gorm.DB) (bool, error) {
	seeded := false
	err := db.Transaction(func(tx *gorm.DB) error {
		for _, model := range []interface{}{&Product{}, &Customer{}, &Order{}} {
			var count int64
			if err := tx.Unscoped().Model(model).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return nil
			}
		}
		var categories []Category
		if err := tx.Order("id ASC").Find(&categories).Error; err != nil {
			return err
		}
		if len(categories) == 0 {
			return nil
		}
		products := []Product{
			{Name: "JBL Tune 520BT", Price: 1790, Quantity: 5},
		}
		for i := range products {
			products[i].CategoryID = categories[i%len(categories)].ID
			if err := createProductDB(tx, &products[i]); err != nil {
				return err
			}
		}
		customers := []Customer{
			{Name: "ชลธิชา รุ่งอรุณ", Email: "chonthicha.shop@example.com", Phone: "0920003001"},
		}
		for i := range customers {
			if err := createCustomerDB(tx, &customers[i]); err != nil {
				return err
			}
		}
		sampleOrders := []struct {
			customerIndex, productIndex, quantity int
			status                                string
		}{
			{0, 0, 1, OrderCompleted},
		}
		for i, sample := range sampleOrders {
			order, err := createOrderDB(tx, customers[sample.customerIndex].ID, products[sample.productIndex].ID, sample.quantity)
			if err != nil {
				return err
			}
			if err := updateOrderStatusDB(tx, order.ID, sample.status); err != nil {
				return err
			}
			// Give each sample order a different date for date-range queries.
			createdAt := time.Now().In(appLocation).AddDate(0, 0, i-len(sampleOrders)+1)
			if err := tx.Model(&Order{}).Where("id = ?", order.ID).Update("created_at", createdAt).Error; err != nil {
				return err
			}
		}
		seeded = true
		return nil
	})
	return seeded && err == nil, err
}

type menuAction struct {
	label string
	run   func() error
}

type console struct{ scanner *bufio.Scanner }

func (ui *console) text(prompt string) (string, error) {
	fmt.Print(prompt + " > ")
	if !ui.scanner.Scan() {
		if err := ui.scanner.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return strings.TrimSpace(ui.scanner.Text()), nil
}

func (ui *console) integer(prompt string) (int, error) {
	value, err := ui.text(prompt)
	if err != nil {
		return 0, err
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("กรุณากรอกจำนวนเต็ม")
	}
	return number, nil
}

func (ui *console) id(prompt string) (uint, error) {
	value, err := ui.text(prompt)
	if err != nil {
		return 0, err
	}
	id, err := strconv.ParseUint(value, 10, strconv.IntSize)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("ID ต้องเป็นจำนวนเต็มบวก")
	}
	return uint(id), nil
}

func (ui *console) money(prompt string) (float64, error) {
	value, err := ui.text(prompt)
	if err != nil {
		return 0, err
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("กรุณากรอกตัวเลข เช่น 1290 หรือ 1290.50")
	}
	if err := validateMoney(number, maxProductPrice); err != nil {
		return 0, err
	}
	return number, nil
}

func (ui *console) priceRange() (float64, float64, error) {
	min, err := ui.money("ราคาตั้งแต่")
	if err != nil {
		return 0, 0, err
	}
	max, err := ui.money("ราคาไม่เกิน")
	if err != nil {
		return 0, 0, err
	}
	return min, max, validatePriceRange(min, max)
}

func (ui *console) date(prompt string) (time.Time, error) {
	value, err := ui.text(prompt)
	if err != nil {
		return time.Time{}, err
	}
	date, err := time.ParseInLocation("2006-01-02", value, appLocation)
	if err != nil {
		return time.Time{}, fmt.Errorf("วันที่ต้องเป็น YYYY-MM-DD เช่น 2026-10-15")
	}
	return date, nil
}

func (ui *console) menu(title string, actions []menuAction) error {
	for {
		fmt.Printf("\n----- %s -----\n", title)
		for i, action := range actions {
			fmt.Printf("[%d] %s\n", i+1, action.label)
		}
		fmt.Println("[0] ย้อนกลับ / ปิดระบบ")
		choice, err := ui.integer("กรอกหมายเลขที่ต้องการ")
		if errors.Is(err, io.EOF) || ui.scanner.Err() != nil {
			return err
		}
		if err != nil {
			fmt.Println("ทำรายการไม่ได้:", err)
			continue
		}
		if choice == 0 {
			return nil
		}
		if choice < 1 || choice > len(actions) {
			fmt.Println("ไม่พบตัวเลือกนี้ กรุณาเลือกใหม่")
			continue
		}
		if err := actions[choice-1].run(); err != nil {
			if errors.Is(err, io.EOF) || ui.scanner.Err() != nil {
				return err
			}
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				fmt.Println("ทำรายการไม่ได้: ไม่พบรายการที่ระบุ หรือรายการถูกนำออกแล้ว")
			case errors.Is(err, gorm.ErrDuplicatedKey):
				fmt.Println("ทำรายการไม่ได้: มีข้อมูลนี้อยู่ในระบบแล้ว โปรดตรวจสอบอีเมลลูกค้า")
			default:
				fmt.Println("ทำรายการไม่ได้:", err)
			}
		} else {
			fmt.Println("เรียบร้อยแล้ว")
		}
	}
}

func run() error {
	config := loadDBConfig()
	db, err := connectDB(config)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	fmt.Printf("ฐานข้อมูลพร้อมใช้งาน: %s (%s:%s)\n", config.Name, config.Host, config.Port)
	seeded, err := seedSampleDataDB(db)
	if err != nil {
		return fmt.Errorf("เพิ่มข้อมูลตัวอย่างไม่สำเร็จ: %w", err)
	}
	if seeded {
		fmt.Println("เตรียมข้อมูลเริ่มต้นแล้ว: สินค้า 1 รายการ | ลูกค้า 1 คน | รายการสั่งซื้อ 1 รายการ")
	} else {
		fmt.Println("โหลดข้อมูลร้านค้าแล้ว จัดการหมวดหมู่สินค้าได้จากตัวเลือก 1")
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	ui := &console{scanner: scanner}
	err = ui.menu("ศูนย์จัดการร้านไอที", []menuAction{
		{"หมวดหมู่สินค้า", func() error { return categoryMenu(db, ui) }},
		{"คลังสินค้าและราคาขาย", func() error { return productMenu(db, ui) }},
		{"ทะเบียนลูกค้า", func() error { return customerMenu(db, ui) }},
		{"ประวัติและรายการสั่งซื้อ", func() error { return orderMenu(db, ui) }},
	})
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "เปิดระบบไม่สำเร็จ:", err)
		os.Exit(1)
	}
}
