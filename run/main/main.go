package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
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

	// Process notifications from the channel
	for notification := range notificationChan {
		signature := notification.Value.Signature

		retry := 1
		maxRetries := 5
		foundData := false
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

			if swapInfo != nil {
				foundData = true

				// marshalledSwapData, _ := json.MarshalIndent(swapInfo, "", "  ")
				// fmt.Println(string(marshalledSwapData))

				transactionInfo := models.Trade{
					Type:            ".",
					DexProvider:     swapInfo.AMMs[0],
					Timestamp:       time.Now().Unix(),
					WalletAddress:   swapInfo.Signers[0].String(),
					TokenInAddress:  swapInfo.TokenOutMint.String(),
					TokenOutAddress: swapInfo.TokenInMint.String(),
					TokenInAmount:   fmt.Sprintf("%d", swapInfo.TokenOutAmount),
					TokenOutAmount:  fmt.Sprintf("%d", swapInfo.TokenInAmount),
					TxID:            signature.String(),
				}

				requestBody, err := json.Marshal(transactionInfo)
				if err != nil {
					fmt.Println("Error marshalling JSON:", err)
					return
				}

				// insert transaction to API
				// Create a new HTTP request
				req, err := http.NewRequest("POST", API_URL, bytes.NewBuffer(requestBody))
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
				fmt.Println("Response:", string(body))

				// unmarshalledSwapData, _ := json.MarshalIndent(transactionInfo, "", "  ")
				// fmt.Println(string(unmarshalledSwapData))
			}

			if foundData {
				break
			}
		}
	}
}

// signature := solana.MustSignatureFromBase58("2FPPbA8UxTyKzGBPagYS8H5ZVkzxJV6uezcYytxg9doRgKhwpNXADKgXs3jCx849NGyrEQgFHgrdNq7dXiBDBXTM")

// swapInfo, err := parseTxData(rpcClient, signature)
// if err != nil {
// 	fmt.Println("Error parsing transaction data: ", err)
// }

// marshalledSwapData, _ := json.MarshalIndent(swapInfo, "", "  ")
// fmt.Println(string(marshalledSwapData))

// tokenInAmountStr := fmt.Sprintf("%d", swapInfo.TokenOutAmount)
// tokenOutAmountStr := fmt.Sprintf("%d", swapInfo.TokenInAmount)
// transactionInfo := models.Trade{
// 	Type:            "b",
// 	DexProvider:     swapInfo.AMMs[0],
// 	Timestamp:       swapInfo.Timestamp.Unix(),
// 	WalletAddress:   walletAddress,
// 	TokenInAddress:  swapInfo.TokenOutMint.String(),
// 	TokenOutAddress: swapInfo.TokenInMint.String(),
// 	TokenInAmount:   tokenInAmountStr,
// 	TokenOutAmount:  tokenOutAmountStr,
// 	TxID:            signature.String(),
// }

// unmarshalledSwapData, _ := json.MarshalIndent(transactionInfo, "", "  ")
// fmt.Println(string(unmarshalledSwapData))

// Subscribe to logs involving the wallet address
// sub, err := wsClient.LogsSubscribeMentions(pubKey, rpc.CommitmentConfirmed)
// if err != nil {
// 	log.Fatalf("Failed to subscribe to logs: %v", err)
// }
// defer sub.Unsubscribe()

// Listen for transactions involving the wallet
// for {
// 	notification, err := sub.Recv(context.Background())
// 	if err != nil {
// 		fmt.Println("Error receiving notification: ", err)
// 		continue
// 	}

// 	signature := notification.Value.Signature
// 	fmt.Printf("Received notification for transaction: %s\n", signature)

// 	swapInfo, err := parseTxData(rpcClient, signature)
// 	if err != nil {
// 		fmt.Println("Error parsing transaction data: ", err)
// 		continue
// 	}

// 	marshalledSwapData, _ := json.MarshalIndent(swapInfo, "", "  ")
// 	fmt.Println(string(marshalledSwapData))
// }
