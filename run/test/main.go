package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/big"
	"os"
	"time"

	solanaswapgo "github.com/franco-bianco/solanaswap-go/solanaswap-go"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/joho/godotenv"
	"github.com/nikola43/solanatxtracker/models"
)

/*
Example Transactions:
- Orca: 2kAW5GAhPZjM3NoSrhJVHdEpwjmq9neWtckWnjopCfsmCGB27e3v2ZyMM79FdsL4VWGEtYSFi1sF1Zhs7bqdoaVT
- Pumpfun: 4Cod1cNGv6RboJ7rSB79yeVCR4Lfd25rFgLY3eiPJfTJjTGyYP1r2i1upAYZHQsWDqUbGd1bhTRm1bpSQcpWMnEz
- Banana Gun: oXUd22GQ1d45a6XNzfdpHAX6NfFEfFa9o2Awn2oimY89Rms3PmXL1uBJx3CnTYjULJw6uim174b3PLBFkaAxKzK
- Jupiter: DBctXdTTtvn7Rr4ikeJFCBz4AtHmJRyjHGQFpE59LuY3Shb7UcRJThAXC7TGRXXskXuu9LEm9RqtU6mWxe5cjPF
- Jupiter DCA: 4mxr44yo5Qi7Rabwbknkh8MNUEWAMKmzFQEmqUVdx5JpHEEuh59TrqiMCjZ7mgZMozRK1zW8me34w8Myi8Qi1tWP
- Meteora DLMM: 125MRda3h1pwGZpPRwSRdesTPiETaKvy4gdiizyc3SWAik4cECqKGw2gggwyA1sb2uekQVkupA2X9S4vKjbstxx3
- Rayd V4: 5kaAWK5X9DdMmsWm6skaUXLd6prFisuYJavd9B62A941nRGcrmwvncg3tRtUfn7TcMLsrrmjCChdEjK3sjxS6YG9
- Rayd Routing: 51nj5GtAmDC23QkeyfCNfTJ6Pdgwx7eq4BARfq1sMmeEaPeLsx9stFA3Dzt9MeLV5xFujBgvghLGcayC3ZevaQYi
- Rayd CPMM: afUCiFQ6amxuxx2AAwsghLt7Q9GYqHfZiF4u3AHhAzs8p1ThzmrtSUFMbcdJy8UnQNTa35Fb1YqxR6F9JMZynYp
- Rayd Concentrated Liquidity SwapV2: 2durZHGFkK4vjpWFGc5GWh5miDs8ke8nWkuee8AUYJA8F9qqT2Um76Q5jGsbK3w2MMgqwZKbnENTLWZoi3d6o2Ds
- Rayd Concentrated Liquidity Swap: 4MSVpVBwxnYTQSF3bSrAB99a3pVr6P6bgoCRDsrBbDMA77WeQqoBDDDXqEh8WpnUy5U4GeotdCG9xyExjNTjYE1u
- Maestro: mWaH4FELcPj4zeY4Cgk5gxUirQDM7yE54VgMEVaqiUDQjStyzwNrxLx4FMEaKEHQoYsgCRhc1YdmBvhGDRVgRrq
- Meteora Pools Program: 4uuw76SPksFw6PvxLFkG9jRyReV1F4EyPYNc3DdSECip8tM22ewqGWJUaRZ1SJEZpuLJz1qPTEPb2es8Zuegng9Z
- Moonshot Buy: AhiFQX1Z3VYbkKQH64ryPDRwxUv8oEPzQVjSvT7zY58UYDm4Yvkkt2Ee9VtSXtF6fJz8fXmb5j3xYVDF17Gr9CG
- Moonshot Sell: 2XYu86VrUXiwNNj8WvngcXGytrCsSrpay69Rt3XBz9YZvCQcZJLjvDfh9UWETFtFW47vi4xG2CkiarRJwSe6VekE
- Multiple AMMs: 46Jp5EEUrmdCVcE3jeewqUmsMHhqiWWtj243UZNDFZ3mmma6h2DF4AkgPE9ToRYVLVrfKQCJphrvxbNk68Lub9vw (not supported yet)
- OKX: 5xaT2SXQUyvyLGsnyyoKMwsDoHrx1enCKofkdRMdNaL5MW26gjQBM3AWebwjTJ49uqEqnFu5d9nXJek6gUSGCqbL
*/

func parseTokenAmount(amount uint64, decimals uint8) *big.Float {
	amountFloat := new(big.Float).SetUint64(amount)
	decimalFactor := new(big.Float).SetFloat64(math.Pow(10, float64(decimals)))
	return new(big.Float).Quo(amountFloat, decimalFactor)
}

func main() {
	// Load environment variables
	if err := godotenv.Load("../../.env"); err != nil {
		log.Printf("Warning: .env file not found, using environment variables")
	}

	// Get RPC URL from environment or use default mainnet
	rpcURL := os.Getenv("RPC_URL")
	if rpcURL == "" {
		rpcURL = rpc.MainNetBeta.RPC
		log.Printf("RPC_URL not set, using default mainnet: %s", rpcURL)
	}

	// Get signature from command line or use default
	signatureStr := "4MAEMzMNjDB2fSdMmXwLKevEHJvN7zQz9GuKKyZrbcFuHW4AbpUDUEta1VNemGegUckTibdUwNt85fhzwhWFqvAD"
	if len(os.Args) > 1 {
		signatureStr = os.Args[1]
	}

	rpcClient := rpc.New(rpcURL)
	signature := solana.MustSignatureFromBase58(signatureStr)

	fmt.Printf("Parsing transaction: %s\n", signature.String())
	fmt.Printf("Using RPC: %s\n\n", rpcURL)

	var maxTxVersion uint64 = 0
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tx, err := rpcClient.GetTransaction(
		ctx,
		signature,
		&rpc.GetTransactionOpts{
			Commitment:                     rpc.CommitmentConfirmed,
			MaxSupportedTransactionVersion: &maxTxVersion,
		},
	)
	if err != nil {
		log.Fatalf("Error getting transaction: %s", err)
	}

	parser, err := solanaswapgo.NewTransactionParser(tx)
	if err != nil {
		log.Fatalf("Error creating parser: %s", err)
	}

	transactionData, err := parser.ParseTransaction()
	if err != nil {
		log.Fatalf("Error parsing transaction: %s", err)
	}

	fmt.Println("=== Raw Transaction Data ===")
	marshalledData, _ := json.MarshalIndent(transactionData, "", "  ")
	fmt.Println(string(marshalledData))

	swapInfo, err := parser.ProcessSwapData(transactionData)
	if err != nil {
		log.Fatalf("Error processing swap data: %s", err)
	}

	fmt.Println("\n=== Processed Swap Info ===")
	marshalledSwapData, _ := json.MarshalIndent(swapInfo, "", "  ")
	fmt.Println(string(marshalledSwapData))

	// Determine trade type
	WSOL := "So11111111111111111111111111111111111111112"
	USDC := "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	USDT := "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"

	tradeType := "BUY"
	tokenOut := swapInfo.TokenOutMint.String()
	if tokenOut == WSOL || tokenOut == USDC || tokenOut == USDT {
		tradeType = "SELL"
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

	fmt.Println("\n=== Trade Info ===")
	unmarshalledSwapData, _ := json.MarshalIndent(transactionInfo, "", "  ")
	fmt.Println(string(unmarshalledSwapData))
}
