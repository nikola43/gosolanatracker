package db

import (
	"fmt"
	"log"

	"github.com/nikola43/solanatxtracker/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var GormDB *gorm.DB

func Migrate() {
	// DROP
	GormDB.Migrator().DropTable(&models.Wallets{})
	GormDB.Migrator().DropTable(&models.Trade{})

	// CREATE
	GormDB.AutoMigrate(&models.Wallets{})
	GormDB.AutoMigrate(&models.Trade{})
}

func InitializeDatabase(user, password, dbName, host, port string, migrate bool) {
	var err error
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s", user, password, host, port, dbName)
	GormDB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Info)})
	if err != nil {
		log.Fatal(err)
	}

	if migrate {
		Migrate()
	}
}
