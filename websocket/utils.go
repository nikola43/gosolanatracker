package websocket

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

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/nikola43/solanatxtracker/db"
	"github.com/nikola43/solanatxtracker/models"
	solanaswapgo "github.com/nikola43/solanatxtracker/solanaswap-go"
)

// parseTokenAmount converts a token amount to a human-readable format
func ParseTokenAmount(amount uint64, decimals uint8) *big.Float {
	amountFloat := new(big.Float).SetUint64(amount)
	decimalFactor := new(big.Float).SetFloat64(math.Pow(10, float64(decimals)))
	return new(big.Float).Quo(amountFloat, decimalFactor)
}

// sendTransactionToAPI sends transaction data to API
func SendTransactionToAPI(apiURL string, transactionInfo models.Trade) {
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

// parseTxData parses transaction data
func ParseTxData(rpcClient *rpc.Client, signature solana.Signature) (*solanaswapgo.SwapInfo, error) {
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

// loadWallets loads all wallets from the database
func LoadWallets() []solana.PublicKey {
	var wallets []models.Wallets
	db.GormDB.Find(&wallets)

	walletsPublicKeys := make([]solana.PublicKey, 0, len(wallets))
	for _, wallet := range wallets {
		pubKey, err := solana.PublicKeyFromBase58(wallet.Address)
		if err != nil {
			log.Printf("Invalid wallet address %s: %v", wallet.Address, err)
			continue
		}
		walletsPublicKeys = append(walletsPublicKeys, pubKey)
	}

	fmt.Printf("Loaded %d wallets\n", len(walletsPublicKeys))
	return walletsPublicKeys
}

