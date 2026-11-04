// Package db provides database models and persistence operations.
package db

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

var (
	db      *gorm.DB
	dbMutex sync.Mutex
	// ErrCloseConnection indicates close database connection failed.
	ErrCloseConnection = errors.New("close database connection failed")
)

// Conn exposes the package's conn value.
//
//nolint:cyclop // This bounded database lifecycle keeps pool ownership and query classification explicit.
func Conn() *gorm.DB {
	dbMutex.Lock()

	if db != nil {
		database := db
		dbMutex.Unlock()
		return database
	}
	// refer https://github.com/go-sql-driver/mysql#dsn-data-source-name for details
	user := viper.GetString("db.username")
	pass := viper.GetString("db.password")
	dbname := viper.GetString("db.database")
	host := viper.GetString("db.host")
	port := viper.GetString("db.port")
	tls := viper.GetBool("db.tls")
	if user == "" || pass == "" || dbname == "" || host == "" || port == "" {
		dbMutex.Unlock()
		log.Fatal().Msg("mysql db config should not empty")
		return nil
	}
	dsn := "%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=UTC"
	if tls {
		dsn += "&tls=true"
	}
	prefix := viper.GetString("db.table.prefix")
	if prefix != "" && !strings.HasSuffix(prefix, "_") {
		prefix += "_"
	}
	dsn = fmt.Sprintf(dsn, user, pass, host, port, dbname)
	var err error
	db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
		Logger:                                   NewLogger(),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix: prefix,
		},
	})
	if err != nil {
		dbMutex.Unlock()
		log.Fatal().Err(err).Send()
		return nil
	}
	migrateDB()
	database := db
	dbMutex.Unlock()
	return database
}

// DB performs the db operation.
func DB() *gorm.DB {
	return Conn()
}

// Close disconnects every connection in the underlying database pool.
// Callers must stop database users before closing the shared pool.
func Close() error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if db == nil {
		return nil
	}
	database := db
	db = nil
	sqlDB, err := database.DB()
	if err != nil {
		return errors.Join(ErrCloseConnection, err)
	}
	if closeErr := sqlDB.Close(); closeErr != nil {
		return errors.Join(ErrCloseConnection, closeErr)
	}
	return nil
}

func migrateDB() {
	err := db.AutoMigrate()
	if err != nil {
		log.Error().Err(err).Msg("db auto migrate failed")
	}
}
