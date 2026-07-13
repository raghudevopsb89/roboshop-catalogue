package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

// productColumns are the columns selected by the product queries, in order.
var productColumns = []string{
	"id", "sku", "name", "description", "price", "image_url", "category", "stock",
}

// newTestRouter builds a gin engine with the same routes as main() but without
// the newrelic / prometheus / logging middleware, so tests exercise the real
// handlers and route registration in isolation.
func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerRoutes(r)
	return r
}

// newMockDB creates a sqlmock-backed *sql.DB, installs it into the package-level
// db variable, and returns the mock plus a cleanup func that restores db.
func newMockDB(t *testing.T) (sqlmock.Sqlmock, func()) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	prev := db
	db = mockDB
	return mock, func() {
		mockDB.Close()
		db = prev
	}
}

func TestHealth(t *testing.T) {
	router := newTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["status"] != "OK" {
		t.Errorf("expected status OK, got %q", body["status"])
	}
	if body["service"] != "catalogue" {
		t.Errorf("expected service catalogue, got %q", body["service"])
	}
}

func TestSearchProducts(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		setupMock  func(sqlmock.Sqlmock)
		wantStatus int
		wantLen    int
	}{
		{
			name:       "missing q returns 400",
			query:      "/products/search",
			setupMock:  func(sqlmock.Sqlmock) {},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:  "with q returns matches",
			query: "/products/search?q=cam",
			setupMock: func(m sqlmock.Sqlmock) {
				rows := sqlmock.NewRows(productColumns).
					AddRow(1, "CAM-1", "Camera", "A camera", 99.5, "http://img/1", "electronics", 10)
				m.ExpectQuery("SELECT (.+) FROM products WHERE name LIKE").
					WithArgs("%cam%", "%cam%").
					WillReturnRows(rows)
			},
			wantStatus: http.StatusOK,
			wantLen:    1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock, cleanup := newMockDB(t)
			defer cleanup()
			tc.setupMock(mock)

			router := newTestRouter()
			req := httptest.NewRequest(http.MethodGet, tc.query, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d (body=%s)", tc.wantStatus, w.Code, w.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				var products []Product
				if err := json.Unmarshal(w.Body.Bytes(), &products); err != nil {
					t.Fatalf("failed to decode products: %v", err)
				}
				if len(products) != tc.wantLen {
					t.Errorf("expected %d products, got %d", tc.wantLen, len(products))
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unmet sqlmock expectations: %v", err)
			}
		})
	}
}

func TestGetProductByID(t *testing.T) {
	t.Run("found returns 200 with product", func(t *testing.T) {
		mock, cleanup := newMockDB(t)
		defer cleanup()

		rows := sqlmock.NewRows(productColumns).
			AddRow(42, "SKU-42", "Widget", "A widget", 12.34, "http://img/42", "tools", 7)
		mock.ExpectQuery("SELECT (.+) FROM products WHERE id = ?").
			WithArgs("42").
			WillReturnRows(rows)

		router := newTestRouter()
		req := httptest.NewRequest(http.MethodGet, "/products/42", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
		}
		var p Product
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatalf("failed to decode product: %v", err)
		}
		if p.ID != 42 || p.SKU != "SKU-42" || p.Name != "Widget" {
			t.Errorf("unexpected product: %+v", p)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet sqlmock expectations: %v", err)
		}
	})

	t.Run("not found returns 404", func(t *testing.T) {
		mock, cleanup := newMockDB(t)
		defer cleanup()

		mock.ExpectQuery("SELECT (.+) FROM products WHERE id = ?").
			WithArgs("999").
			WillReturnError(sql.ErrNoRows)

		router := newTestRouter()
		req := httptest.NewRequest(http.MethodGet, "/products/999", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d (body=%s)", w.Code, w.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet sqlmock expectations: %v", err)
		}
	})
}

func TestGetProducts(t *testing.T) {
	mock, cleanup := newMockDB(t)
	defer cleanup()

	rows := sqlmock.NewRows(productColumns).
		AddRow(1, "SKU-1", "Alpha", "First", 1.0, "http://img/1", "cat-a", 5).
		AddRow(2, "SKU-2", "Beta", "Second", 2.0, "http://img/2", "cat-b", 3)
	mock.ExpectQuery("SELECT (.+) FROM products").
		WillReturnRows(rows)

	router := newTestRouter()
	req := httptest.NewRequest(http.MethodGet, "/products", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var products []Product
	if err := json.Unmarshal(w.Body.Bytes(), &products); err != nil {
		t.Fatalf("failed to decode products: %v", err)
	}
	if len(products) != 2 {
		t.Fatalf("expected 2 products, got %d", len(products))
	}
	if products[0].Name != "Alpha" || products[1].Name != "Beta" {
		t.Errorf("unexpected products: %+v", products)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestGetCategories(t *testing.T) {
	mock, cleanup := newMockDB(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"category"}).
		AddRow("electronics").
		AddRow("tools")
	mock.ExpectQuery("SELECT DISTINCT category FROM products").
		WillReturnRows(rows)

	router := newTestRouter()
	req := httptest.NewRequest(http.MethodGet, "/categories", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var categories []string
	if err := json.Unmarshal(w.Body.Bytes(), &categories); err != nil {
		t.Fatalf("failed to decode categories: %v", err)
	}
	if len(categories) != 2 || categories[0] != "electronics" || categories[1] != "tools" {
		t.Errorf("unexpected categories: %+v", categories)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestScanProducts(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer mockDB.Close()

	expected := sqlmock.NewRows(productColumns).
		AddRow(7, "SKU-7", "Gamma", "Third", 3.5, "http://img/7", "cat-c", 9)
	mock.ExpectQuery("SELECT (.+) FROM products").WillReturnRows(expected)

	rows, err := mockDB.Query("SELECT id, sku, name, description, price, image_url, category, stock FROM products")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	defer rows.Close()

	products := scanProducts(rows)
	if len(products) != 1 {
		t.Fatalf("expected 1 product, got %d", len(products))
	}
	p := products[0]
	if p.ID != 7 || p.SKU != "SKU-7" || p.Name != "Gamma" || p.Price != 3.5 || p.Stock != 9 {
		t.Errorf("unexpected product: %+v", p)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}
