package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/fatih/color"
	solanaswapgo "github.com/franco-bianco/solanaswap-go/solanaswap-go"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/ws"
	"github.com/joho/godotenv"
	"github.com/nikola43/solanatxtracker/db"
	"github.com/nikola43/solanatxtracker/models"
)

// Config holds all application configuration
type Config struct {
	MySQLHost     string
	MySQLUser     string
	MySQLPassword string
	MySQLDatabase string
	MySQLPort     string
	RPCURL        string
	RPCWS         string
	APIURL        string
}

// Well-known Solana token addresses
const (
	WSOL = "So11111111111111111111111111111111111111112"
	USDC = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	USDT = "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"
)

// Default configuration values
const (
	DefaultReconnectInterval = 10 * time.Second
	DefaultMaxRetries        = 10
	DefaultMaxConcurrent     = 50
	DefaultHTTPTimeout       = 30 * time.Second
	DefaultWSConnectTimeout  = 30 * time.Second
	DefaultParseRetryDelay   = 10 * time.Second
	DefaultParseMaxRetries   = 5
	NotificationBufferSize   = 100
)

// loadConfig loads and validates configuration from environment variables
func loadConfig() (*Config, error) {
	// Load .env file if it exists (optional in production)
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env file not found, using environment variables")
	}

	config := &Config{
		MySQLHost:     os.Getenv("MYSQL_HOST"),
		MySQLUser:     os.Getenv("MYSQL_USER"),
		MySQLPassword: os.Getenv("MYSQL_PASSWORD"),
		MySQLDatabase: os.Getenv("MYSQL_DATABASE"),
		MySQLPort:     os.Getenv("MYSQL_PORT"),
		RPCURL:        os.Getenv("RPC_URL"),
		RPCWS:         os.Getenv("RPC_WS"),
		APIURL:        os.Getenv("API_URL"),
	}

	// Validate required configuration
	if config.MySQLHost == "" {
		return nil, fmt.Errorf("MYSQL_HOST is required")
	}
	if config.MySQLUser == "" {
		return nil, fmt.Errorf("MYSQL_USER is required")
	}
	if config.MySQLDatabase == "" {
		return nil, fmt.Errorf("MYSQL_DATABASE is required")
	}
	if config.MySQLPort == "" {
		config.MySQLPort = "3306" // Default MySQL port
	}
	if config.RPCURL == "" {
		return nil, fmt.Errorf("RPC_URL is required")
	}
	if config.RPCWS == "" {
		return nil, fmt.Errorf("RPC_WS is required")
	}
	if config.APIURL == "" {
		return nil, fmt.Errorf("API_URL is required")
	}

	return config, nil
}

// parseTokenAmount converts a token amount to a human-readable format
func parseTokenAmount(amount uint64, decimals uint8) *big.Float {
	amountFloat := new(big.Float).SetUint64(amount)
	decimalFactor := new(big.Float).SetFloat64(math.Pow(10, float64(decimals)))
	return new(big.Float).Quo(amountFloat, decimalFactor)
}

// parseTxData parses transaction data with proper context handling
func parseTxData(ctx context.Context, rpcClient *rpc.Client, signature solana.Signature) (*solanaswapgo.SwapInfo, error) {
	var maxTxVersion uint64 = 0
	tx, err := rpcClient.GetTransaction(
		ctx,
		signature,
		&rpc.GetTransactionOpts{
			Commitment:                     rpc.CommitmentFinalized,
			MaxSupportedTransactionVersion: &maxTxVersion,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error getting transaction: %w", err)
	}

	parser, err := solanaswapgo.NewTransactionParser(tx)
	if err != nil {
		return nil, fmt.Errorf("error creating parser: %w", err)
	}

	transactionData, err := parser.ParseTransaction()
	if err != nil {
		return nil, fmt.Errorf("error parsing transaction: %w", err)
	}

	swapInfo, err := parser.ProcessSwapData(transactionData)
	if err != nil {
		return nil, fmt.Errorf("error processing swap data: %w", err)
	}

	return swapInfo, nil
}

// HTTPClient wraps http.Client with production-ready defaults
type HTTPClient struct {
	client *http.Client
}

// NewHTTPClient creates a new HTTP client with proper timeouts
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{
		client: &http.Client{
			Timeout: DefaultHTTPTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// sendTransactionToAPI sends transaction data to API with proper error handling
func (hc *HTTPClient) sendTransactionToAPI(ctx context.Context, apiURL string, transactionInfo models.Trade) error {
	requestBody, err := json.Marshal(transactionInfo)
	if err != nil {
		return fmt.Errorf("error marshalling JSON: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(requestBody))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := hc.client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("API returned non-success status %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("API Response for tx %s: %s", transactionInfo.TxID, string(body))
	return nil
}

// WebSocketManager manages WebSocket connections and reconnections
type WebSocketManager struct {
	rpcURL            string
	wsURL             string
	apiURL            string
	walletPublicKeys  []solana.PublicKey
	notificationChan  chan *ws.LogResult
	rpcClient         *rpc.Client
	wsClient          *ws.Client
	subscriptions     []*ws.LogSubscription
	shutdownChan      chan struct{}
	reconnectInterval time.Duration
	maxRetries        int
	mutex             sync.RWMutex
	running           bool
	httpClient        *HTTPClient
	wg                sync.WaitGroup
}

// NewWebSocketManager creates a new WebSocketManager
func NewWebSocketManager(rpcURL, wsURL, apiURL string, walletPublicKeys []solana.PublicKey) *WebSocketManager {
	return &WebSocketManager{
		rpcURL:            rpcURL,
		wsURL:             wsURL,
		apiURL:            apiURL,
		walletPublicKeys:  walletPublicKeys,
		notificationChan:  make(chan *ws.LogResult, NotificationBufferSize),
		shutdownChan:      make(chan struct{}),
		reconnectInterval: DefaultReconnectInterval,
		maxRetries:        DefaultMaxRetries,
		running:           false,
		httpClient:        NewHTTPClient(),
	}
}

// Start starts the WebSocketManager
func (wsm *WebSocketManager) Start() error {
	wsm.mutex.Lock()
	if wsm.running {
		wsm.mutex.Unlock()
		return fmt.Errorf("WebSocketManager is already running")
	}
	wsm.running = true
	wsm.mutex.Unlock()

	wsm.rpcClient = rpc.New(wsm.rpcURL)

	// Start connection monitor
	wsm.wg.Add(1)
	go wsm.monitorAndReconnect()

	return nil
}

// Stop stops the WebSocketManager gracefully
func (wsm *WebSocketManager) Stop() {
	wsm.mutex.Lock()
	if !wsm.running {
		wsm.mutex.Unlock()
		return
	}
	wsm.running = false
	wsm.mutex.Unlock()

	close(wsm.shutdownChan)
	wsm.cleanupConnections()

	// Wait for all goroutines to finish with timeout
	done := make(chan struct{})
	go func() {
		wsm.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("WebSocketManager stopped gracefully")
	case <-time.After(10 * time.Second):
		log.Println("WebSocketManager stop timed out")
	}
}

// IsRunning returns whether the manager is running
func (wsm *WebSocketManager) IsRunning() bool {
	wsm.mutex.RLock()
	defer wsm.mutex.RUnlock()
	return wsm.running
}

// cleanupConnections cleans up WebSocket connections
func (wsm *WebSocketManager) cleanupConnections() {
	wsm.mutex.Lock()
	defer wsm.mutex.Unlock()

	for _, sub := range wsm.subscriptions {
		if sub != nil {
			sub.Unsubscribe()
		}
	}
	wsm.subscriptions = nil

	if wsm.wsClient != nil {
		wsm.wsClient.Close()
		wsm.wsClient = nil
	}
}

// monitorAndReconnect monitors the connection and reconnects if necessary
func (wsm *WebSocketManager) monitorAndReconnect() {
	defer wsm.wg.Done()

	retryCount := 0
	for {
		if !wsm.IsRunning() {
			return
		}

		err := wsm.connect()
		if err != nil {
			retryCount++
			if retryCount > wsm.maxRetries {
				log.Printf("Max retries (%d) exceeded, giving up on WebSocket connection", wsm.maxRetries)
				return
			}
			log.Printf("Failed to connect to WebSocket (attempt %d/%d): %v. Retrying in %v...",
				retryCount, wsm.maxRetries, err, wsm.reconnectInterval)

			select {
			case <-wsm.shutdownChan:
				return
			case <-time.After(wsm.reconnectInterval):
				continue
			}
		}

		// Reset retry count on successful connection
		retryCount = 0

		// Wait for shutdown signal
		<-wsm.shutdownChan
		return
	}
}

// connect establishes a WebSocket connection and sets up subscriptions
func (wsm *WebSocketManager) connect() error {
	wsm.mutex.Lock()
	defer wsm.mutex.Unlock()

	// Clean up existing connections
	for _, sub := range wsm.subscriptions {
		if sub != nil {
			sub.Unsubscribe()
		}
	}
	wsm.subscriptions = nil

	if wsm.wsClient != nil {
		wsm.wsClient.Close()
		wsm.wsClient = nil
	}

	// Create a new WebSocket client with timeout
	ctx, cancel := context.WithTimeout(context.Background(), DefaultWSConnectTimeout)
	defer cancel()

	var err error
	wsm.wsClient, err = ws.Connect(ctx, wsm.wsURL)
	if err != nil {
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}

	// Create subscriptions for each wallet
	wsm.subscriptions = make([]*ws.LogSubscription, len(wsm.walletPublicKeys))
	for i, pubKey := range wsm.walletPublicKeys {
		log.Printf("Subscribing to logs for wallet: %s", pubKey.String())
		sub, err := wsm.wsClient.LogsSubscribeMentions(
			pubKey,
			rpc.CommitmentConfirmed,
		)
		if err != nil {
			return fmt.Errorf("failed to subscribe to logs for wallet %s: %w", pubKey.String(), err)
		}
		wsm.subscriptions[i] = sub

		// Start a goroutine to handle notifications for this subscription
		wsm.wg.Add(1)
		go wsm.handleSubscription(sub, pubKey)
	}

	log.Println(color.GreenString("WebSocket connections established successfully"))
	return nil
}

// handleSubscription handles a single subscription
func (wsm *WebSocketManager) handleSubscription(sub *ws.LogSubscription, pubKey solana.PublicKey) {
	defer wsm.wg.Done()

	for {
		select {
		case <-wsm.shutdownChan:
			return
		default:
			notification, err := sub.Recv(context.Background())
			if err != nil {
				if !wsm.IsRunning() {
					return
				}
				log.Printf("Error receiving notification for wallet %s: %v", pubKey.String(), err)

				// Trigger reconnection by closing the connection
				wsm.mutex.Lock()
				if wsm.wsClient != nil {
					wsm.wsClient.Close()
					wsm.wsClient = nil
				}
				wsm.mutex.Unlock()

				// Give time for reconnection to happen
				time.Sleep(1 * time.Second)
				return
			}

			// Send notification to the channel (non-blocking with timeout)
			select {
			case wsm.notificationChan <- notification:
			case <-time.After(5 * time.Second):
				log.Printf("Warning: notification channel full, dropping notification for tx: %s",
					notification.Value.Signature)
			case <-wsm.shutdownChan:
				return
			}
		}
	}
}

// ProcessNotifications processes notifications from the channel
func (wsm *WebSocketManager) ProcessNotifications(ctx context.Context) {
	semaphore := make(chan struct{}, DefaultMaxConcurrent)

	for {
		select {
		case <-ctx.Done():
			return
		case <-wsm.shutdownChan:
			return
		case notification := <-wsm.notificationChan:
			// Acquire semaphore spot
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				return
			case <-wsm.shutdownChan:
				return
			}

			wsm.wg.Add(1)
			go func(notification *ws.LogResult) {
				defer wsm.wg.Done()
				defer func() { <-semaphore }()

				wsm.processTransaction(ctx, notification)
			}(notification)
		}
	}
}

// processTransaction processes a single transaction notification
func (wsm *WebSocketManager) processTransaction(ctx context.Context, notification *ws.LogResult) {
	signature := notification.Value.Signature
	log.Printf("Received notification for transaction: %s", signature)

	for retry := 1; retry <= DefaultParseMaxRetries; retry++ {
		select {
		case <-ctx.Done():
			return
		case <-wsm.shutdownChan:
			return
		default:
		}

		swapInfo, err := parseTxData(ctx, wsm.rpcClient, signature)
		if err != nil {
			log.Printf("Error parsing transaction data for %s: %v", signature, err)
			if retry < DefaultParseMaxRetries {
				log.Printf("Retrying %s in %v... (Attempt %d/%d)",
					signature, DefaultParseRetryDelay, retry, DefaultParseMaxRetries)
				select {
				case <-time.After(DefaultParseRetryDelay):
					continue
				case <-ctx.Done():
					return
				case <-wsm.shutdownChan:
					return
				}
			}
			log.Printf("Giving up on transaction %s after %d attempts", signature, DefaultParseMaxRetries)
			return
		}

		if swapInfo == nil {
			log.Printf("No swap info found for transaction %s", signature)
			return
		}

		// Determine trade type
		tradeType := "BUY"
		tokenOut := swapInfo.TokenOutMint.String()
		if tokenOut == WSOL || tokenOut == USDC || tokenOut == USDT {
			tradeType = "SELL"
		}

		// Validate AMMs array
		if len(swapInfo.AMMs) == 0 {
			log.Printf("Warning: No AMM found for transaction %s", signature)
			return
		}

		// Validate Signers array
		if len(swapInfo.Signers) == 0 {
			log.Printf("Warning: No signer found for transaction %s", signature)
			return
		}

		transactionInfo := models.Trade{
			Type:            tradeType,
			DexProvider:     swapInfo.AMMs[0],
			Timestamp:       time.Now().Unix(),
			WalletAddress:   swapInfo.Signers[0].String(),
			TokenInAddress:  swapInfo.TokenInMint.String(),
			TokenOutAddress: swapInfo.TokenOutMint.String(),
			TokenInAmount:   parseTokenAmount(swapInfo.TokenInAmount, swapInfo.TokenInDecimals).String(),
			TokenOutAmount:  parseTokenAmount(swapInfo.TokenOutAmount, swapInfo.TokenOutDecimals).String(),
			TxID:            signature.String(),
		}

		if err := wsm.httpClient.sendTransactionToAPI(ctx, wsm.apiURL, transactionInfo); err != nil {
			log.Printf("Error sending transaction to API: %v", err)
		}
		return
	}
}

func main() {
	// Load configuration
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	// System info
	numCPU := runtime.NumCPU()
	runtime.GOMAXPROCS(numCPU)

	fmt.Println()
	fmt.Println(color.YellowString("  ----------------- System Info -----------------"))
	fmt.Println(color.CyanString("\t    Number CPU cores available: "), color.GreenString(strconv.Itoa(numCPU)))
	fmt.Println(color.MagentaString("\t    Used CPU cores: "), color.YellowString(strconv.Itoa(numCPU)))
	fmt.Println()

	// Initialize database connection
	if err := db.InitializeDatabase(
		config.MySQLUser,
		config.MySQLPassword,
		config.MySQLDatabase,
		config.MySQLHost,
		config.MySQLPort,
		false,
	); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Get all wallets
	var wallets []models.Wallets
	if err := db.GormDB.Find(&wallets).Error; err != nil {
		log.Fatalf("Failed to fetch wallets: %v", err)
	}

	if len(wallets) == 0 {
		log.Println("Warning: No wallets found in database. Add wallets to start tracking.")
	}

	walletPublicKeys := make([]solana.PublicKey, 0, len(wallets))
	for _, wallet := range wallets {
		pubKey, err := solana.PublicKeyFromBase58(wallet.Address)
		if err != nil {
			log.Printf("Warning: Invalid wallet address %s: %v", wallet.Address, err)
			continue
		}
		walletPublicKeys = append(walletPublicKeys, pubKey)
	}

	// Initialize WebSocket Manager
	wsManager := NewWebSocketManager(config.RPCURL, config.RPCWS, config.APIURL, walletPublicKeys)

	// Start the WebSocket Manager
	if err := wsManager.Start(); err != nil {
		log.Fatalf("Failed to start WebSocket Manager: %v", err)
	}

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start processing notifications
	go wsManager.ProcessNotifications(ctx)

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println(color.GreenString("Service running. Press Ctrl+C to stop."))

	// Wait for interrupt signal
	sig := <-sigChan
	log.Printf("Received signal %v, shutting down gracefully...", sig)

	// Cancel context and stop manager
	cancel()
	wsManager.Stop()

	log.Println("Service stopped")
}
