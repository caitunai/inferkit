package redis

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/spf13/viper"
)

var (
	rdb      *redis.Client
	rdbMutex sync.Mutex
	// ErrCloseConnection indicates close redis connection failed.
	ErrCloseConnection = errors.New("close redis connection failed")
	// ErrConnect indicates connect redis failed.
	ErrConnect = errors.New("connect redis failed")
	// ErrInvalidConfig indicates invalid redis configuration.
	ErrInvalidConfig = errors.New("invalid redis configuration")
)

type borrowedClient struct {
	redis.UniversalClient
}

// Close leaves the shared client open because the redis package owns it.
func (borrowedClient) Close() error {
	return nil
}

// Init initializes package resources.
func Init(ctx context.Context) error {
	rdbMutex.Lock()
	if rdb == nil {
		if err := validateConfig(); err != nil {
			rdbMutex.Unlock()
			return err
		}
		rdb = newClient()
	}
	client := rdb
	rdbMutex.Unlock()

	pingTimeout := viper.GetDuration("redis.pingTimeout")
	if pingTimeout <= 0 {
		pingTimeout = 3 * time.Second
	}
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		closeErr := closeClient(client)
		return errors.Join(ErrConnect, err, closeErr)
	}
	return nil
}

func newClient() *redis.Client {
	host := viper.GetString("redis.host")
	port := viper.GetString("redis.port")
	options := &redis.Options{
		Addr:            net.JoinHostPort(host, port),
		Username:        viper.GetString("redis.username"),
		Password:        viper.GetString("redis.password"),
		DB:              viper.GetInt("redis.db"),
		DialTimeout:     viper.GetDuration("redis.dialTimeout"),
		ReadTimeout:     viper.GetDuration("redis.readTimeout"),
		WriteTimeout:    viper.GetDuration("redis.writeTimeout"),
		PoolTimeout:     viper.GetDuration("redis.poolTimeout"),
		PoolSize:        viper.GetInt("redis.poolSize"),
		MinIdleConns:    viper.GetInt("redis.minIdleConnections"),
		MaxIdleConns:    viper.GetInt("redis.maxIdleConnections"),
		ConnMaxIdleTime: viper.GetDuration("redis.connectionMaxIdleTime"),
		ConnMaxLifetime: viper.GetDuration("redis.connectionMaxLifetime"),
	}
	if viper.GetBool("redis.tls.enabled") {
		serverName := viper.GetString("redis.tls.serverName")
		if serverName == "" {
			serverName = host
		}
		options.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: serverName,
		}
	}
	return redis.NewClient(options)
}

// GetClient returns client.
func GetClient() *redis.Client {
	rdbMutex.Lock()
	defer rdbMutex.Unlock()

	if rdb == nil {
		rdb = newClient()
	}
	return rdb
}

func validateConfig() error {
	if viper.GetString("redis.host") == "" || viper.GetString("redis.port") == "" {
		return ErrInvalidConfig
	}
	return nil
}

// GetBorrowedClient returns the shared Redis client without transferring
// ownership. Calling Close on the returned client does not close the pool.
func GetBorrowedClient() redis.UniversalClient {
	return borrowedClient{UniversalClient: GetClient()}
}

// Close disconnects every connection in the shared Redis client pool.
// Callers must stop Redis users before closing the shared client.
func Close() error {
	rdbMutex.Lock()
	defer rdbMutex.Unlock()

	if rdb == nil {
		return nil
	}
	client := rdb
	rdb = nil
	if err := client.Close(); err != nil {
		return errors.Join(ErrCloseConnection, err)
	}
	return nil
}

func closeClient(client *redis.Client) error {
	rdbMutex.Lock()
	if rdb == client {
		rdb = nil
	}
	rdbMutex.Unlock()
	if err := client.Close(); err != nil {
		return errors.Join(ErrCloseConnection, err)
	}
	return nil
}
