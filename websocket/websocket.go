package websocket

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go/rpc"

	"github.com/fatih/color"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc/ws"
	"github.com/nikola43/solanatxtracker/models"
)

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
					swapInfo, err := ParseTxData(wsm.RpcClient, signature)
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
							TokenInAmount:   ParseTokenAmount(swapInfo.TokenInAmount, swapInfo.TokenInDecimals).String(),
							TokenOutAmount:  ParseTokenAmount(swapInfo.TokenOutAmount, swapInfo.TokenOutDecimals).String(),
							TxID:            signature.String(),
						}

						// Send the transaction to API
						SendTransactionToAPI(wsm.ApiURL, transactionInfo)
					}
					break
				}
			}(notification)
		}
	}
}
