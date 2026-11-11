package db

import (
	"database/sql"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestMySQLDSNUsesUTC(t *testing.T) {
	t.Parallel()
	for _, tls := range []bool{false, true} {
		config, err := mysqldriver.ParseDSN(mysqlDSN("user", "password", "127.0.0.1", "3306", "test", tls))
		if err != nil {
			t.Fatal(err)
		}
		if !config.ParseTime || config.Loc != time.UTC || config.Params["time_zone"] != "'+00:00'" {
			t.Fatal("MySQL sessions and Go time decoding must both use UTC")
		}
		if (config.TLSConfig == "true") != tls {
			t.Fatal("UTC connection options changed the TLS setting")
		}
	}
}

func TestCloseDisconnectsDatabasePool(t *testing.T) {
	sqlDB, err := sql.Open("mysql", "user:password@tcp(127.0.0.1:1)/test")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if operationErr := sqlDB.Close(); operationErr != nil {
			t.Errorf("sql.DB.Close() cleanup error = %v", operationErr)
		}
	})
	database, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}

	dbMutex.Lock()
	db = database
	dbMutex.Unlock()

	if operationErr := Close(); operationErr != nil {
		t.Fatalf("Close() error = %v", operationErr)
	}
	if operationErr := sqlDB.PingContext(t.Context()); operationErr == nil {
		t.Fatal("Ping() succeeded after Close()")
	}
	if operationErr := Close(); operationErr != nil {
		t.Fatalf("second Close() error = %v", operationErr)
	}
}
