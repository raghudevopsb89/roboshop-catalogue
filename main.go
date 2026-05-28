package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
)

type Product struct {
	ID          int64   `json:"id"`
	SKU         string  `json:"sku"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	ImageURL    string  `json:"imageUrl"`
	Category    string  `json:"category"`
	Stock       int     `json:"stock"`
}

var db *sql.DB

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func connectDB() {
	mysqlHost := getEnv("MYSQL_HOST", "mysql")
	mysqlUser := getEnv("MYSQL_USER", "catalogue")
	mysqlPass := getEnv("MYSQL_PASSWORD", "RoboShop@1")
	mysqlDB := getEnv("MYSQL_DATABASE", "catalogue")

	dsn := fmt.Sprintf("%s:%s@tcp(%s:3306)/%s?parseTime=true", mysqlUser, mysqlPass, mysqlHost, mysqlDB)

	var err error
	for i := 0; i < 30; i++ {
		db, err = sql.Open("mysql", dsn)
		if err == nil {
			err = db.Ping()
			if err == nil {
				log.Println("Connected to MySQL")
				return
			}
		}
		log.Printf("MySQL connection attempt %d/30 failed: %v, retrying in 2s...", i+1, err)
		time.Sleep(2 * time.Second)
	}
	log.Fatal("Failed to connect to MySQL:", err)
}

func main() {
	connectDB()
	defer db.Close()

	r := gin.Default()
	r.Use(cors.Default())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "OK", "service": "catalogue"})
	})

	r.GET("/products", getProducts)
	r.GET("/products/search", searchProducts)
	r.GET("/products/:id", getProductByID)
	r.GET("/categories", getCategories)

	port := getEnv("PORT", "8002")
	log.Printf("Catalogue service listening on port %s", port)
	r.Run(":" + port)
}

func getProducts(c *gin.Context) {
	category := c.Query("category")
	var rows *sql.Rows
	var err error

	if category != "" {
		rows, err = db.Query("SELECT id, sku, name, description, price, image_url, category, stock FROM products WHERE category = ?", category)
	} else {
		rows, err = db.Query("SELECT id, sku, name, description, price, image_url, category, stock FROM products")
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	products := scanProducts(rows)
	c.JSON(http.StatusOK, products)
}

func searchProducts(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'q' is required"})
		return
	}

	rows, err := db.Query(
		"SELECT id, sku, name, description, price, image_url, category, stock FROM products WHERE name LIKE ? OR description LIKE ?",
		"%"+query+"%", "%"+query+"%",
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	products := scanProducts(rows)
	c.JSON(http.StatusOK, products)
}

func getProductByID(c *gin.Context) {
	id := c.Param("id")
	var p Product
	err := db.QueryRow(
		"SELECT id, sku, name, description, price, image_url, category, stock FROM products WHERE id = ?", id,
	).Scan(&p.ID, &p.SKU, &p.Name, &p.Description, &p.Price, &p.ImageURL, &p.Category, &p.Stock)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Product not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

func getCategories(c *gin.Context) {
	rows, err := db.Query("SELECT DISTINCT category FROM products ORDER BY category")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var categories []string
	for rows.Next() {
		var cat string
		rows.Scan(&cat)
		categories = append(categories, cat)
	}
	c.JSON(http.StatusOK, categories)
}

func scanProducts(rows *sql.Rows) []Product {
	var products []Product
	for rows.Next() {
		var p Product
		rows.Scan(&p.ID, &p.SKU, &p.Name, &p.Description, &p.Price, &p.ImageURL, &p.Category, &p.Stock)
		products = append(products, p)
	}
	if products == nil {
		products = []Product{}
	}
	return products
}
