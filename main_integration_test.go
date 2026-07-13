//go:build integration

// Package main integration test.
//
// This is a COMPONENT INTEGRATION test: it spins up a REAL MySQL 8 server in a
// throwaway Docker container (via testcontainers-go), loads the actual project
// schema (db/schema.sql) and seed data (db/master-data.sql) into it, wires the
// gin router through registerRoutes, and drives the real HTTP handlers with
// httptest against the real database. There is NO sqlmock here.
//
// It is guarded by the `integration` build tag so that a plain `go test ./...`
// (the unit suite in main_test.go) neither compiles nor runs it and therefore
// does not require Docker. Run it with:
//
//	go test -tags=integration ./... -v
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
)

// integrationRouter builds the same router main() uses (routes registered via
// registerRoutes) in gin.TestMode, against whatever the package-global db points
// at — which TestMain has pointed at the real container.
func integrationRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerRoutes(r)
	return r
}

// TestMain starts one MySQL 8 container for the whole integration suite, loads
// schema + seed data, installs the connection into the package-global db, runs
// the tests, then tears everything down.
func TestMain(m *testing.M) {
	ctx := context.Background()

	// Start a real MySQL 8 server. The container's database is named "catalogue"
	// to match what schema.sql / master-data.sql and the app queries expect.
	container, err := tcmysql.Run(ctx,
		"mysql:8.0",
		tcmysql.WithDatabase("catalogue"),
		tcmysql.WithUsername("root"),
		tcmysql.WithPassword("test"),
	)
	if err != nil {
		log.Fatalf("failed to start mysql container: %v", err)
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Printf("failed to terminate container: %v", err)
		}
	}()

	// Build a DSN using the PLAIN "mysql" driver (github.com/go-sql-driver/mysql).
	// The production code opens the "nrmysql" New Relic wrapper driver, which
	// needs New Relic initialisation; for tests we bypass that and talk to the
	// same MySQL over the vanilla driver. multiStatements lets us exec the .sql
	// files (which contain several statements each) in one Exec call.
	dsn, err := container.ConnectionString(ctx, "parseTime=true", "multiStatements=true")
	if err != nil {
		log.Fatalf("failed to build connection string: %v", err)
	}

	testDB, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("failed to open db: %v", err)
	}

	// Wait until the server actually answers.
	if err := pingUntilReady(ctx, testDB); err != nil {
		log.Fatalf("mysql never became ready: %v", err)
	}

	// Load the REAL project schema then the REAL seed data, in that order.
	// We deliberately run them manually (rather than via init scripts) so the
	// ordering schema -> data is guaranteed.
	for _, script := range []string{
		filepath.Join("db", "schema.sql"),
		filepath.Join("db", "master-data.sql"),
	} {
		sqlBytes, err := os.ReadFile(script)
		if err != nil {
			log.Fatalf("failed to read %s: %v", script, err)
		}
		if _, err := testDB.Exec(string(sqlBytes)); err != nil {
			log.Fatalf("failed to exec %s: %v", script, err)
		}
	}

	// Install the real DB into the package global that the handlers use, keeping
	// the previous value so we restore it afterwards (good hygiene / symmetry
	// with the unit test's newMockDB).
	prev := db
	db = testDB

	code := m.Run()

	db = prev
	_ = testDB.Close()

	os.Exit(code)
}

func pingUntilReady(ctx context.Context, d *sql.DB) error {
	var lastErr error
	for i := 0; i < 30; i++ {
		if err := d.PingContext(ctx); err != nil {
			lastErr = err
			time.Sleep(time.Second)
			continue
		}
		return nil
	}
	return fmt.Errorf("still not ready after retries: %w", lastErr)
}

// doGET drives one request through the real router and returns the recorder.
func doGET(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	integrationRouter().ServeHTTP(w, req)
	return w
}

// TestIntegration_GetProducts asserts the seeded rows come back from a REAL query.
func TestIntegration_GetProducts(t *testing.T) {
	w := doGET(t, "/products")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var products []Product
	if err := json.Unmarshal(w.Body.Bytes(), &products); err != nil {
		t.Fatalf("failed to decode products: %v", err)
	}

	// master-data.sql seeds exactly 12 products (ROB001..ROB012).
	if len(products) != 12 {
		t.Fatalf("expected 12 seeded products, got %d", len(products))
	}

	// Index by SKU and spot-check known seed values.
	bySKU := map[string]Product{}
	for _, p := range products {
		bySKU[p.SKU] = p
	}

	rob001, ok := bySKU["ROB001"]
	if !ok {
		t.Fatalf("expected seeded product ROB001 to be present; got SKUs %v", keys(bySKU))
	}
	if rob001.Name != "Robo-Arm Deluxe" {
		t.Errorf("ROB001 name: want %q, got %q", "Robo-Arm Deluxe", rob001.Name)
	}
	if rob001.Price != 1299.99 {
		t.Errorf("ROB001 price: want %v, got %v", 1299.99, rob001.Price)
	}
	if rob001.Category != "Robots" {
		t.Errorf("ROB001 category: want %q, got %q", "Robots", rob001.Category)
	}
	if rob001.Stock != 25 {
		t.Errorf("ROB001 stock: want %d, got %d", 25, rob001.Stock)
	}

	// Every seeded product must have a real auto-increment id (>0).
	for _, p := range products {
		if p.ID <= 0 {
			t.Errorf("product %s has non-positive id %d", p.SKU, p.ID)
		}
	}
}

// TestIntegration_GetProductByID_Found looks up a known seeded product by its
// real auto-increment id (discovered from the list response, so we don't hard
// code the id) and asserts the row matches the seed file.
func TestIntegration_GetProductByID_Found(t *testing.T) {
	// Discover the id assigned to ROB005 by the real database.
	list := doGET(t, "/products")
	var products []Product
	if err := json.Unmarshal(list.Body.Bytes(), &products); err != nil {
		t.Fatalf("failed to decode products: %v", err)
	}
	var wantID int64
	for _, p := range products {
		if p.SKU == "ROB005" {
			wantID = p.ID
		}
	}
	if wantID == 0 {
		t.Fatalf("could not find seeded product ROB005 to derive its id")
	}

	w := doGET(t, fmt.Sprintf("/products/%d", wantID))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var p Product
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("failed to decode product: %v", err)
	}
	if p.ID != wantID {
		t.Errorf("id: want %d, got %d", wantID, p.ID)
	}
	if p.SKU != "ROB005" {
		t.Errorf("sku: want ROB005, got %q", p.SKU)
	}
	if p.Name != "RoboOS Pro License" {
		t.Errorf("name: want %q, got %q", "RoboOS Pro License", p.Name)
	}
	if p.Category != "Software" {
		t.Errorf("category: want Software, got %q", p.Category)
	}
	if p.Price != 599.99 {
		t.Errorf("price: want 599.99, got %v", p.Price)
	}
}

// TestIntegration_GetProductByID_NotFound proves the real sql.ErrNoRows -> 404 path.
func TestIntegration_GetProductByID_NotFound(t *testing.T) {
	w := doGET(t, "/products/999999")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["error"] != "Product not found" {
		t.Errorf("error: want %q, got %q", "Product not found", body["error"])
	}
}

// TestIntegration_SearchProducts exercises the REAL LIKE query against the real
// schema + data, covering a name match, a description-only match, and no match.
func TestIntegration_SearchProducts(t *testing.T) {
	tests := []struct {
		name     string
		q        string
		wantSKUs []string // expected, order-independent
	}{
		{
			// "Gripper" appears in the NAME of ROB010 only.
			name:     "name match",
			q:        "Gripper",
			wantSKUs: []string{"ROB010"},
		},
		{
			// "aerospace" appears only in the DESCRIPTION of ROB006
			// ("aerospace-grade titanium"), proving the OR description LIKE clause.
			name:     "description-only match",
			q:        "aerospace",
			wantSKUs: []string{"ROB006"},
		},
		{
			// "Servo" appears in the NAME of ROB003 only.
			name:     "single name match servo",
			q:        "Servo",
			wantSKUs: []string{"ROB003"},
		},
		{
			name:     "no match",
			q:        "no-such-thing-xyz",
			wantSKUs: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doGET(t, "/products/search?q="+tc.q)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
			}
			var products []Product
			if err := json.Unmarshal(w.Body.Bytes(), &products); err != nil {
				t.Fatalf("failed to decode products: %v", err)
			}
			got := map[string]bool{}
			for _, p := range products {
				got[p.SKU] = true
			}
			if len(products) != len(tc.wantSKUs) {
				t.Fatalf("q=%q: want %d matches %v, got %d %v",
					tc.q, len(tc.wantSKUs), tc.wantSKUs, len(products), skus(products))
			}
			for _, sku := range tc.wantSKUs {
				if !got[sku] {
					t.Errorf("q=%q: expected match %s missing; got %v", tc.q, sku, skus(products))
				}
			}
		})
	}
}

// TestIntegration_GetCategories asserts the real DISTINCT ... ORDER BY category
// returns exactly the 5 seeded categories in sorted order.
func TestIntegration_GetCategories(t *testing.T) {
	w := doGET(t, "/categories")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var categories []string
	if err := json.Unmarshal(w.Body.Bytes(), &categories); err != nil {
		t.Fatalf("failed to decode categories: %v", err)
	}

	// Distinct categories in master-data.sql, MySQL case-insensitive sort order.
	want := []string{"Accessories", "AI Modules", "Components", "Robots", "Software"}
	if len(categories) != len(want) {
		t.Fatalf("want %d categories %v, got %d %v", len(want), want, len(categories), categories)
	}
	for i := range want {
		if categories[i] != want[i] {
			t.Errorf("category[%d]: want %q, got %q (full=%v)", i, want[i], categories[i], categories)
		}
	}
}

func keys(m map[string]Product) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func skus(products []Product) []string {
	out := make([]string, 0, len(products))
	for _, p := range products {
		out = append(out, p.SKU)
	}
	return out
}
