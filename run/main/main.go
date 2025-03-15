package main

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"

	"github.com/fatih/color"
	"github.com/joho/godotenv"
	"github.com/nikola43/solanatxtracker/db"
	"github.com/nikola43/solanatxtracker/websocket"
)

func main() {
	// get environment variables
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}
	MYSQL_HOST := os.Getenv("MYSQL_HOST")
	MYSQL_USER := os.Getenv("MYSQL_USER")
	MYSQL_PASSWORD := os.Getenv("MYSQL_PASSWORD")
	MYSQL_DATABASE := os.Getenv("MYSQL_DATABASE")
	MYSQL_PORT := os.Getenv("MYSQL_PORT")
	RPC_URL := os.Getenv("RPC_URL")
	RPC_WS := os.Getenv("RPC_WS")
	API_URL := os.Getenv("API_URL")

	// system config
	numCpu := runtime.NumCPU()
	usedCpu := numCpu
	runtime.GOMAXPROCS(usedCpu)
	fmt.Println("")
	fmt.Println(color.YellowString("  ----------------- System Info -----------------"))
	fmt.Println(color.CyanString("\t    Number CPU cores available: "), color.GreenString(strconv.Itoa(numCpu)))
	fmt.Println(color.MagentaString("\t    Used of CPU cores: "), color.YellowString(strconv.Itoa(usedCpu)))
	fmt.Println(color.MagentaString(""))

	// initialize database connection
	db.InitializeDatabase(MYSQL_USER, MYSQL_PASSWORD, MYSQL_DATABASE, MYSQL_HOST, MYSQL_PORT, false)

	// Load initial wallets
	walletsPublicKeys := websocket.LoadWallets()

	// Initialize WebSocket Manager
	wsManager := websocket.NewWebSocketManager(RPC_URL, RPC_WS, API_URL, walletsPublicKeys)

	// Start the WebSocket Manager
	err = wsManager.Start()
	if err != nil {
		log.Fatalf("Failed to start WebSocket Manager: %v", err)
	}

	// Start processing notifications
	go wsManager.ProcessNotifications()

	// Wait for interrupt signal
	fmt.Println(color.GreenString("Service running. Press Ctrl+C to stop."))

	// Wait forever (or until interrupted)
	select {}
}
