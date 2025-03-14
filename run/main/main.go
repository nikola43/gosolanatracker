package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"math/big"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
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

// parseTokenAmount converts a token amount to a human-readable format
func parseTokenAmount(amount uint64, decimals uint8) *big.Float {
	amountFloat := new(big.Float).SetUint64(amount)
	decimalFactor := new(big.Float).SetFloat64(math.Pow(10, float64(decimals)))
	return new(big.Float).Quo(amountFloat, decimalFactor)
}

// parseTxData parses transaction data
func parseTxData(rpcClient *rpc.Client, signature solana.Signature) (*solanaswapgo.SwapInfo, error) {
	var maxTxVersion uint64 = 0
	tx, err := rpcClient.GetTransaction(
		context.TODO(),
		signature,
		&rpc.GetTransactionOpts{
			Commitment:                     rpc.CommitmentFinalized,
			MaxSupportedTransactionVersion: &maxTxVersion,
		},
	)

	if err != nil {
		fmt.Println("Error getting transaction: ", err)
		return nil, err
	}

	parser, err := solanaswapgo.NewTransactionParser(tx)
	if err != nil {
		fmt.Println("error creating parser: ", err)
		return nil, err
	}

	transactionData, err := parser.ParseTransaction()
	if err != nil {
		fmt.Println("error parsing transaction: ", err)
		return nil, err
	}

	swapInfo, err := parser.ProcessSwapData(transactionData)
	if err != nil {
		fmt.Println("error processing swap data: ", err)
		return nil, err
	}

	return swapInfo, nil
}

// sendTransactionToAPI sends transaction data to API
func sendTransactionToAPI(apiURL string, transactionInfo models.Trade) {
	requestBody, err := json.Marshal(transactionInfo)
	if err != nil {
		fmt.Println("Error marshalling JSON:", err)
		return
	}

	// Create a new HTTP request
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(requestBody))
	if err != nil {
		fmt.Println("Error creating request:", err)
		return
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")

	// Create an HTTP client and send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Error sending request:", err)
		return
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Error reading response:", err)
		return
	}

	// Print the response
	fmt.Printf("API Response for tx %s: %s\n", transactionInfo.TxID, string(body))
}

// WebSocketManager manages WebSocket connections and reconnections
type WebSocketManager struct {
	RpcURL            string
	WsURL             string
	ApiURL            string
	WalletPublicKeys  []solana.PublicKey
	NotificationChan  chan *ws.LogResult
	RpcClient         *rpc.Client
	WsClient          *ws.Client
	Subscriptions     []*ws.LogSubscription
	ShutdownChan      chan struct{}
	ReconnectInterval time.Duration
	MaxRetries        int
	Mutex             sync.Mutex
	Running           bool
}

// NewWebSocketManager creates a new WebSocketManager
func NewWebSocketManager(rpcURL, wsURL, apiURL string, walletPublicKeys []solana.PublicKey) *WebSocketManager {
	return &WebSocketManager{
		RpcURL:            rpcURL,
		WsURL:             wsURL,
		ApiURL:            apiURL,
		WalletPublicKeys:  walletPublicKeys,
		NotificationChan:  make(chan *ws.LogResult, 100), // Buffer size to prevent blocking
		ShutdownChan:      make(chan struct{}),
		ReconnectInterval: 10 * time.Second,
		MaxRetries:        10,
		Running:           false,
	}
}

// Start starts the WebSocketManager
func (wsm *WebSocketManager) Start() error {
	wsm.Mutex.Lock()
	if wsm.Running {
		wsm.Mutex.Unlock()
		return fmt.Errorf("WebSocketManager is already running")
	}
	wsm.Running = true
	wsm.Mutex.Unlock()

	wsm.RpcClient = rpc.New(wsm.RpcURL)

	// Start connection monitor
	go wsm.monitorAndReconnect()

	return nil
}

// Stop stops the WebSocketManager
func (wsm *WebSocketManager) Stop() {
	wsm.Mutex.Lock()
	defer wsm.Mutex.Unlock()

	if !wsm.Running {
		return
	}

	close(wsm.ShutdownChan)
	wsm.cleanupConnections()
	wsm.Running = false
}

// cleanupConnections cleans up WebSocket connections
func (wsm *WebSocketManager) cleanupConnections() {
	// Unsubscribe from all subscriptions
	for _, sub := range wsm.Subscriptions {
		if sub != nil {
			sub.Unsubscribe()
		}
	}
	wsm.Subscriptions = nil

	// Close WebSocket client
	if wsm.WsClient != nil {
		wsm.WsClient.Close()
		wsm.WsClient = nil
	}
}

// monitorAndReconnect monitors the connection and reconnects if necessary
func (wsm *WebSocketManager) monitorAndReconnect() {
	for {
		// Try to connect
		err := wsm.connect()
		if err != nil {
			fmt.Printf("Failed to connect to WebSocket: %v. Retrying in %v...\n", err, wsm.ReconnectInterval)
			select {
			case <-wsm.ShutdownChan:
				return
			case <-time.After(wsm.ReconnectInterval):
				continue
			}
		}

		// Wait for shutdown signal or reconnection trigger
		select {
		case <-wsm.ShutdownChan:
			return
		}
	}
}

// connect establishes a WebSocket connection and sets up subscriptions
func (wsm *WebSocketManager) connect() error {
	wsm.Mutex.Lock()
	defer wsm.Mutex.Unlock()

	// Clean up existing connections
	wsm.cleanupConnections()

	// Create a new WebSocket client
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var err error
	wsm.WsClient, err = ws.Connect(ctx, wsm.WsURL)
	if err != nil {
		return fmt.Errorf("failed to connect to WebSocket: %v", err)
	}

	// Create subscriptions for each wallet
	wsm.Subscriptions = make([]*ws.LogSubscription, len(wsm.WalletPublicKeys))
	for i, pubKey := range wsm.WalletPublicKeys {
		fmt.Printf("Subscribing to logs for wallet: %s\n", pubKey.String())
		sub, err := wsm.WsClient.LogsSubscribeMentions(
			pubKey,
			rpc.CommitmentConfirmed,
		)
		if err != nil {
			return fmt.Errorf("failed to subscribe to logs for wallet %s: %v", pubKey.String(), err)
		}
		wsm.Subscriptions[i] = sub

		// Start a goroutine to handle notifications for this subscription
		go wsm.handleSubscription(sub, pubKey)
	}

	fmt.Println(color.GreenString("WebSocket connections established successfully"))
	return nil
}

// handleSubscription handles a single subscription
func (wsm *WebSocketManager) handleSubscription(sub *ws.LogSubscription, pubKey solana.PublicKey) {
	for {
		notification, err := sub.Recv(context.Background())
		if err != nil {
			fmt.Printf("Error receiving notification for wallet %s: %v\n", pubKey.String(), err)
			
			// Trigger reconnection by closing the connection
			wsm.Mutex.Lock()
			if wsm.WsClient != nil {
				wsm.WsClient.Close()
				wsm.WsClient = nil
			}
			wsm.Mutex.Unlock()
			
			// Allow time for monitorAndReconnect to notice and start reconnection
			time.Sleep(1 * time.Second)
			return
		}

		// Send notification to the channel
		wsm.NotificationChan <- notification
	}
}

// ProcessNotifications processes notifications from the channel
func (wsm *WebSocketManager) ProcessNotifications() {
	// Create a semaphore to limit concurrent processing
	const maxConcurrent = 50
	semaphore := make(chan struct{}, maxConcurrent)

	// Process notifications from the channel
	for {
		select {
		case <-wsm.ShutdownChan:
			return
		case notification := <-wsm.NotificationChan:
			// Acquire semaphore spot
			semaphore <- struct{}{}

			// Process transaction in a separate goroutine
			go func(notification *ws.LogResult) {
				defer func() {
					// Release the semaphore when done
					<-semaphore
				}()

				signature := notification.Value.Signature
				fmt.Printf("Received notification for transaction: %s\n", signature)

				retry := 1
				maxRetries := 5
				for retry <= maxRetries {
					swapInfo, err := parseTxData(wsm.RpcClient, signature)
					if err != nil {
						fmt.Printf("Error parsing transaction data for %s: %v\n", signature, err)
						if retry < maxRetries {
							fmt.Printf("Retrying %s in 10 seconds... (Attempt %d/%d)\n",
								signature, retry, maxRetries)
							retry++
							time.Sleep(10 * time.Second)
							continue
						}
						fmt.Printf("Giving up on transaction %s after %d attempts\n",
							signature, maxRetries)
						return
					}

					WSOL := "So11111111111111111111111111111111111111112"
					USDC := "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
					USDT := "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"

					tradeType := "BUY"
					if swapInfo.TokenOutMint.String() == WSOL || swapInfo.TokenOutMint.String() == USDC || swapInfo.TokenOutMint.String() == USDT {
						tradeType = "SELL"
					}

					if swapInfo != nil {
						// Process the swap info
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

						// Send the transaction to API
						sendTransactionToAPI(wsm.ApiURL, transactionInfo)
					}
					break
				}
			}(notification)
		}
	}
}

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

	// get all wallets
	wallets := []models.Wallets{}
	db.GormDB.Find(&wallets)
	walletsPublicKeys := make([]solana.PublicKey, len(wallets))
	for i, wallet := range wallets {
		pubKey, err := solana.PublicKeyFromBase58(wallet.Address)
		if err != nil {
			log.Fatalf("Invalid wallet address: %v", err)
		}
		walletsPublicKeys[i] = pubKey
	}

	// Initialize WebSocket Manager
	wsManager := NewWebSocketManager(RPC_URL, RPC_WS, API_URL, walletsPublicKeys)
	
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