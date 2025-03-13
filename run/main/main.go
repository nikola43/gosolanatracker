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

func parseTokenAmount(amount uint64, decimals uint8) *big.Float {
	amountFloat := new(big.Float).SetUint64(amount)
	decimalFactor := new(big.Float).SetFloat64(math.Pow(10, float64(decimals)))
	return new(big.Float).Quo(amountFloat, decimalFactor)
}

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

	// 5YZtbCXqJ2BHo9kgvP5Um6gxLQkzfqvjJcSYbHCswC9EvgoCmLU1CQCJLG47cjAb5S4mRCBsFA1X7t74cV95CGVR

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

	// initialize RPC and Websocket clients
	rpcClient := rpc.New(RPC_URL)
	_ = rpcClient
	wsClient, err := ws.Connect(context.Background(), RPC_WS)
	if err != nil {
		log.Fatalf("Failed to connect to Solana WebSocket: %v", err)
	}
	defer wsClient.Close()

	// Subscribe to logs involving the wallet address
	subscriptions := make([]*ws.LogSubscription, len(walletsPublicKeys))
	for i, pubKey := range walletsPublicKeys {
		fmt.Printf("Subscribing to logs for wallet: %s\n", pubKey.String())
		sub, err := wsClient.LogsSubscribeMentions(pubKey, rpc.CommitmentConfirmed)
		if err != nil {
			log.Fatalf("Failed to subscribe to logs: %v", err)
		}
		defer sub.Unsubscribe()
		subscriptions[i] = sub
	}

	// Create a channel to receive notifications
	notificationChan := make(chan *ws.LogResult)

	// Create a WaitGroup to track goroutines
	var wg sync.WaitGroup

	// Start a Goroutine for each subscription
	for _, sub := range subscriptions {
		go func(sub *ws.LogSubscription) {
			for {
				notification, err := sub.Recv(context.Background())
				if err != nil {
					fmt.Println("Error receiving notification: ", err)
					continue
				}
				notificationChan <- notification
			}
		}(sub)
	}

	// Process notifications from the channel in parallel
	go func() {
		for notification := range notificationChan {
			wg.Add(1) // Increment WaitGroup counter

			go func(signature solana.Signature) {
				defer wg.Done() // Decrement WaitGroup counter when done

				retry := 1
				maxRetries := 5

				for retry <= maxRetries {
					fmt.Printf("Received notification for transaction: %s\n", signature)
					swapInfo, err := parseTxData(rpcClient, signature)
					if err != nil {
						fmt.Println("Error parsing transaction data: ", err)
						fmt.Println("Retrying in 10 seconds...")
						retry++
						time.Sleep(10 * time.Second)
						continue
					}

					if swapInfo == nil {
						fmt.Println("No swap data found, retrying...")
						retry++
						time.Sleep(10 * time.Second)
						continue
					}


					// Process the transaction only if swapInfo is not nil
					transactionInfo := models.Trade{
						Type:            ".",
						DexProvider:     swapInfo.AMMs[0],
						Timestamp:       time.Now().Unix(),
						WalletAddress:   swapInfo.Signers[0].String(),
						TokenInAddress:  swapInfo.TokenInMint.String(),
						TokenOutAddress: swapInfo.TokenOutMint.String(),
						TokenInAmount:   parseTokenAmount(swapInfo.TokenInAmount, swapInfo.TokenInDecimals).String(),
						TokenOutAmount:  parseTokenAmount(swapInfo.TokenOutAmount, swapInfo.TokenOutDecimals).String(),
						TxID:            signature.String(),
					}

					requestBody, err := json.Marshal(transactionInfo)
					if err != nil {
						fmt.Println("Error marshalling JSON:", err)
						return
					}

					// Send the transaction data to API
					req, err := http.NewRequest("POST", API_URL, bytes.NewBuffer(requestBody))
					if err != nil {
						fmt.Println("Error creating request:", err)
						return
					}

					req.Header.Set("Content-Type", "application/json")

					client := &http.Client{}
					resp, err := client.Do(req)
					if err != nil {
						fmt.Println("Error sending request:", err)
						return
					}
					defer resp.Body.Close()

					body, err := ioutil.ReadAll(resp.Body)
					if err != nil {
						fmt.Println("Error reading response:", err)
						return
					}

					fmt.Println("Response:", string(body))

					// If you reached here, it means the transaction was successfully processed
					break
				}
			}(notification.Value.Signature)
		}
	}()

	// Wait for all goroutines to finish processing
	wg.Wait()
}
