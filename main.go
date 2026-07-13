package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/newrelic/go-agent/v3/integrations/nrgin"
	_ "github.com/newrelic/go-agent/v3/integrations/nrmysql"
	"github.com/newrelic/go-agent/v3/newrelic"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var jlog = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

var reqSeq atomic.Uint64

func structuredLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/metrics" || c.Request.URL.Path == "/health" {
			c.Next()
			return
		}
		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			reqID = fmt.Sprintf("%d-%d", os.Getpid(), reqSeq.Add(1))
		}
		c.Writer.Header().Set("X-Request-ID", reqID)
		start := time.Now()
		jlog.Info("req.start",
			"service", "catalogue", "reqId", reqID,
			"method", c.Request.Method, "path", c.Request.URL.Path, "remote", c.ClientIP())
		c.Next()
		event := "finish"
		select {
		case <-c.Request.Context().Done():
			event = "close"
		default:
		}
		jlog.Info("req."+event,
			"service", "catalogue", "reqId", reqID,
			"method", c.Request.Method, "path", c.Request.URL.Path,
			"status", c.Writer.Status(), "durMs", float64(time.Since(start).Microseconds())/1000.0)
	}
}

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

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)
	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Duration of HTTP requests in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path", "status"},
	)
)

func prometheusMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/metrics" {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()

		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		status := strconv.Itoa(c.Writer.Status())
		labels := prometheus.Labels{
			"method": c.Request.Method,
			"path":   path,
			"status": status,
		}
		httpRequestsTotal.With(labels).Inc()
		httpRequestDuration.With(labels).Observe(time.Since(start).Seconds())
	}
}

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
		db, err = sql.Open("nrmysql", dsn)
		if err == nil {
			err = db.Ping()
			if err == nil {
				jlog.Info("Connected to MySQL", "service", "catalogue")
				return
			}
		}
		jlog.Warn("MySQL connection attempt failed", "service", "catalogue", "attempt", i+1, "error", err)
		time.Sleep(2 * time.Second)
	}
	log.Fatal("Failed to connect to MySQL:", err)
}

func main() {
	connectDB()
	defer db.Close()

	nrApp, err := newrelic.NewApplication(
		newrelic.ConfigAppName(getEnv("NEW_RELIC_APP_NAME", "roboshop-catalogue")),
		newrelic.ConfigLicense(os.Getenv("NEW_RELIC_LICENSE_KEY")),
		newrelic.ConfigDistributedTracerEnabled(true),
		newrelic.ConfigAppLogForwardingEnabled(true),
	)
	if err != nil {
		jlog.Warn("newrelic init failed", "service", "catalogue", "error", err)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	if nrApp != nil {
		r.Use(nrgin.Middleware(nrApp))
	}
	r.Use(cors.Default())
	r.Use(structuredLogger())
	r.Use(prometheusMiddleware())

	registerRoutes(r)

	port := getEnv("PORT", "8002")
	srv := &http.Server{Addr: ":" + port, Handler: r}

	go func() {
		jlog.Info("server.listen", "service", "catalogue", "port", port, "pid", os.Getpid())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			jlog.Error("server.listen.error", "service", "catalogue", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	sig := <-stop
	jlog.Warn("server.shutdown.start", "service", "catalogue", "signal", sig.String())
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		jlog.Error("server.shutdown.error", "service", "catalogue", "error", err)
		os.Exit(1)
	}
	jlog.Info("server.shutdown.done", "service", "catalogue", "signal", sig.String())
}

// registerRoutes wires all HTTP routes onto the provided engine. It is called
// by main() and re-used by tests so both exercise identical route registration.
func registerRoutes(r *gin.Engine) {
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "OK", "service": "catalogue"})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.GET("/products", getProducts)
	r.GET("/products/search", searchProducts)
	r.GET("/products/:id", getProductByID)
	r.GET("/categories", getCategories)
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
