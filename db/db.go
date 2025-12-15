package db

import (
	"fmt"
	"log"
	"time"

	"github.com/nikola43/solanatxtracker/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// GormDB is the global database connection
var GormDB *gorm.DB

// DatabaseConfig holds database configuration options
type DatabaseConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// DefaultDatabaseConfig returns sensible defaults for production
func DefaultDatabaseConfig() *DatabaseConfig {
	return &DatabaseConfig{
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
	}
}

// Migrate drops and recreates tables (use with caution in production)
func Migrate() error {
	if GormDB == nil {
		return fmt.Errorf("database not initialized")
	}

	// DROP tables
	if err := GormDB.Migrator().DropTable(&models.Wallets{}); err != nil {
		log.Printf("Warning: failed to drop Wallets table: %v", err)
	}
	if err := GormDB.Migrator().DropTable(&models.Trade{}); err != nil {
		log.Printf("Warning: failed to drop Trade table: %v", err)
	}

	// CREATE tables
	if err := GormDB.AutoMigrate(&models.Wallets{}); err != nil {
		return fmt.Errorf("failed to migrate Wallets: %w", err)
	}
	if err := GormDB.AutoMigrate(&models.Trade{}); err != nil {
		return fmt.Errorf("failed to migrate Trade: %w", err)
	}

	return nil
}

// AutoMigrate creates or updates tables without dropping existing data
func AutoMigrate() error {
	if GormDB == nil {
		return fmt.Errorf("database not initialized")
	}

	if err := GormDB.AutoMigrate(&models.Wallets{}, &models.Trade{}); err != nil {
		return fmt.Errorf("failed to auto-migrate: %w", err)
	}

	return nil
}

// InitializeDatabase connects to MySQL and configures connection pooling
func InitializeDatabase(user, password, dbName, host, port string, migrate bool) error {
	return InitializeDatabaseWithConfig(user, password, dbName, host, port, migrate, DefaultDatabaseConfig())
}

// InitializeDatabaseWithConfig connects to MySQL with custom configuration
func InitializeDatabaseWithConfig(user, password, dbName, host, port string, migrate bool, config *DatabaseConfig) error {
	// Build DSN with proper options for production
	// parseTime=True - parse MySQL datetime to Go time.Time
	// charset=utf8mb4 - full Unicode support
	// loc=Local - use local timezone
	// timeout=10s - connection timeout
	// readTimeout=30s - read timeout
	// writeTimeout=30s - write timeout
	dsn := fmt.Sprintf(
		"%s:%s@tcp(%s:%s)/%s?parseTime=True&charset=utf8mb4&loc=Local&timeout=10s&readTimeout=30s&writeTimeout=30s",
		user, password, host, port, dbName,
	)

	var err error
	GormDB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn), // Less verbose in production
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
		PrepareStmt: true, // Cache prepared statements for better performance
	})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// Get underlying sql.DB to configure connection pool
	sqlDB, err := GormDB.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying DB: %w", err)
	}

	// Configure connection pool
	if config != nil {
		sqlDB.SetMaxOpenConns(config.MaxOpenConns)
		sqlDB.SetMaxIdleConns(config.MaxIdleConns)
		sqlDB.SetConnMaxLifetime(config.ConnMaxLifetime)
		sqlDB.SetConnMaxIdleTime(config.ConnMaxIdleTime)
	}

	// Verify connection
	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	log.Printf("Database connection established to %s:%s/%s", host, port, dbName)

	if migrate {
		if err := Migrate(); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
		log.Println("Database migration completed")
	}

	return nil
}

// Close closes the database connection
func Close() error {
	if GormDB == nil {
		return nil
	}

	sqlDB, err := GormDB.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying DB: %w", err)
	}

	return sqlDB.Close()
}

// HealthCheck verifies database connectivity
func HealthCheck() error {
	if GormDB == nil {
		return fmt.Errorf("database not initialized")
	}

	sqlDB, err := GormDB.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying DB: %w", err)
	}

	return sqlDB.Ping()
}
