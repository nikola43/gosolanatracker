# Go Solana Tracker

A high-performance, production-ready Solana blockchain swap transaction tracker written in Go. Monitor wallet activity in real-time, parse swap transactions from multiple DEX platforms, and forward trade data to external APIs.

## Features

- **Real-time Transaction Monitoring** - WebSocket-based subscription to Solana blockchain events
- **Multi-Wallet Support** - Track multiple wallets simultaneously
- **DEX Support** - Parse swaps from major DEX platforms:
  - Jupiter (including DCA)
  - Raydium (V4, CPMM, Concentrated Liquidity)
  - Orca
  - Meteora (DLMM, Pools)
  - PumpFun
  - Moonshot
  - OKX DEX
  - Maestro
  - Banana Gun
- **Automatic Trade Classification** - Identifies BUY/SELL based on token output
- **Production-Ready** - Connection pooling, graceful shutdown, retry logic
- **API Integration** - Forward parsed trades to external endpoints

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Go Solana Tracker                          │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌─────────────┐    ┌──────────────┐    ┌─────────────────────┐   │
│  │   MySQL     │◄───│  Database    │    │   WebSocket         │   │
│  │  Database   │    │   Layer      │    │   Manager           │   │
│  └─────────────┘    └──────────────┘    └──────────┬──────────┘   │
│                                                     │              │
│                     ┌──────────────┐                │              │
│                     │  Transaction │◄───────────────┘              │
│                     │   Parser     │                               │
│                     └──────┬───────┘                               │
│                            │                                       │
│                     ┌──────▼───────┐    ┌─────────────────────┐   │
│                     │    Trade     │───►│   External API      │   │
│                     │   Builder    │    │   (HTTP POST)       │   │
│                     └──────────────┘    └─────────────────────┘   │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

## Requirements

- Go 1.21 or higher
- MySQL 8.0 or higher
- Solana RPC endpoint (HTTP + WebSocket)

## Quick Start

### 1. Clone the Repository

```bash
git clone https://github.com/nikola43/gosolanatracker.git
cd gosolanatracker
```

### 2. Install Dependencies

```bash
go mod download
```

### 3. Configure Environment

```bash
# Copy the example environment file
cp .env.example .env

# Edit with your configuration
nano .env
```

### 4. Setup Database

Create a MySQL database:

```sql
CREATE DATABASE solana_tracker CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'tracker'@'localhost' IDENTIFIED BY 'your_secure_password';
GRANT ALL PRIVILEGES ON solana_tracker.* TO 'tracker'@'localhost';
FLUSH PRIVILEGES;
```

Add wallets to track:

```sql
USE solana_tracker;

INSERT INTO wallets (address, label, active) VALUES
('YourWalletAddress1...', 'Whale Wallet 1', true),
('YourWalletAddress2...', 'DEX Trader', true);
```

### 5. Build and Run

```bash
# Build the application
go build -o tracker ./run/main

# Run the tracker
./tracker
```

## Configuration

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `MYSQL_HOST` | Yes | - | MySQL server hostname |
| `MYSQL_PORT` | No | `3306` | MySQL server port |
| `MYSQL_USER` | Yes | - | MySQL username |
| `MYSQL_PASSWORD` | No | - | MySQL password |
| `MYSQL_DATABASE` | Yes | - | Database name |
| `RPC_URL` | Yes | - | Solana RPC HTTP endpoint |
| `RPC_WS` | Yes | - | Solana WebSocket endpoint |
| `API_URL` | Yes | - | External API endpoint for trades |

### Recommended RPC Providers

For production use, consider these RPC providers:

| Provider | Free Tier | Notes |
|----------|-----------|-------|
| [Helius](https://helius.xyz) | 100K credits/month | Excellent for WebSocket |
| [QuickNode](https://quicknode.com) | 10M credits/month | Low latency |
| [Alchemy](https://alchemy.com) | 300M CU/month | Good reliability |
| [Triton](https://triton.one) | Pay-per-use | Enterprise grade |

## API Integration

The tracker sends parsed trades to your configured `API_URL` via HTTP POST:

### Request Format

```json
{
  "tx_id": "5xaT2SXQ...",
  "type": "BUY",
  "dex_provider": "Jupiter",
  "timestamp": 1702656000,
  "wallet_address": "7xKXt...",
  "token_in_address": "So111...",
  "token_out_address": "EPjF...",
  "token_in_amount": "1.5",
  "token_out_amount": "150.25"
}
```

### Trade Types

- `BUY` - Token purchased (output is not SOL/USDC/USDT)
- `SELL` - Token sold (output is SOL, USDC, or USDT)

## Database Schema

### `wallets` Table

| Column | Type | Description |
|--------|------|-------------|
| `id` | UINT | Primary key |
| `address` | VARCHAR(44) | Solana wallet address |
| `label` | VARCHAR(100) | Optional description |
| `active` | BOOL | Enable/disable tracking |
| `created_at` | TIMESTAMP | Record creation time |
| `updated_at` | TIMESTAMP | Last update time |

### `trades` Table

| Column | Type | Description |
|--------|------|-------------|
| `id` | UINT | Primary key |
| `tx_id` | VARCHAR(88) | Transaction signature |
| `type` | VARCHAR(4) | BUY or SELL |
| `dex_provider` | VARCHAR(50) | DEX name |
| `timestamp` | BIGINT | Unix timestamp |
| `wallet_address` | VARCHAR(44) | Trader wallet |
| `token_in_address` | VARCHAR(44) | Input token mint |
| `token_out_address` | VARCHAR(44) | Output token mint |
| `token_in_amount` | VARCHAR(50) | Human-readable amount |
| `token_out_amount` | VARCHAR(50) | Human-readable amount |
| `created_at` | TIMESTAMP | Record creation time |

## Testing

Parse a specific transaction:

```bash
# Build test tool
go build -o parse-tx ./run/test

# Parse a transaction (uses RPC_URL from .env or default mainnet)
./parse-tx 5xaT2SXQUyvyLGsnyyoKMwsDoHrx1enCKofkdRMdNaL5MW26gjQBM3AWebwjTJ49uqEqnFu5d9nXJek6gUSGCqbL
```

### Example Transactions for Testing

```bash
# Jupiter swap
./parse-tx DBctXdTTtvn7Rr4ikeJFCBz4AtHmJRyjHGQFpE59LuY3Shb7UcRJThAXC7TGRXXskXuu9LEm9RqtU6mWxe5cjPF

# Raydium V4
./parse-tx 5kaAWK5X9DdMmsWm6skaUXLd6prFisuYJavd9B62A941nRGcrmwvncg3tRtUfn7TcMLsrrmjCChdEjK3sjxS6YG9

# PumpFun
./parse-tx 4Cod1cNGv6RboJ7rSB79yeVCR4Lfd25rFgLY3eiPJfTJjTGyYP1r2i1upAYZHQsWDqUbGd1bhTRm1bpSQcpWMnEz

# Orca
./parse-tx 2kAW5GAhPZjM3NoSrhJVHdEpwjmq9neWtckWnjopCfsmCGB27e3v2ZyMM79FdsL4VWGEtYSFi1sF1Zhs7bqdoaVT
```

## Production Deployment

### Using systemd (Linux)

Create `/etc/systemd/system/solana-tracker.service`:

```ini
[Unit]
Description=Go Solana Tracker
After=network.target mysql.service

[Service]
Type=simple
User=tracker
WorkingDirectory=/opt/solana-tracker
ExecStart=/opt/solana-tracker/tracker
Restart=always
RestartSec=5
Environment=MYSQL_HOST=localhost
Environment=MYSQL_PORT=3306
Environment=MYSQL_USER=tracker
Environment=MYSQL_PASSWORD=your_password
Environment=MYSQL_DATABASE=solana_tracker
Environment=RPC_URL=https://your-rpc-endpoint
Environment=RPC_WS=wss://your-ws-endpoint
Environment=API_URL=https://your-api-endpoint

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable solana-tracker
sudo systemctl start solana-tracker
sudo systemctl status solana-tracker
```

### Using Docker

```dockerfile
FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o tracker ./run/main

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/tracker .
CMD ["./tracker"]
```

```bash
docker build -t solana-tracker .
docker run -d --env-file .env --name tracker solana-tracker
```

## Monitoring

The application logs to stdout. Key log messages:

```
# Successful connection
WebSocket connections established successfully

# Transaction received
Received notification for transaction: 5xaT2...

# API success
API Response for tx 5xaT2...: {"status":"ok"}

# Graceful shutdown
Received signal interrupt, shutting down gracefully...
WebSocketManager stopped gracefully
Service stopped
```

## Troubleshooting

### WebSocket Connection Issues

```
Failed to connect to WebSocket: connection refused
```

- Verify `RPC_WS` URL is correct
- Check if your RPC provider supports WebSocket
- Ensure you haven't exceeded rate limits

### Database Connection Issues

```
failed to connect to database: dial tcp: connect: connection refused
```

- Verify MySQL is running: `sudo systemctl status mysql`
- Check credentials in `.env`
- Ensure database exists

### Transaction Parse Errors

```
error parsing transaction: failed to parse instruction
```

- Some complex multi-AMM transactions aren't supported
- Transaction may not be a swap

## Acknowledgments

- [solanaswap-go](https://github.com/franco-bianco/solanaswap-go) - Swap transaction parsing
- [solana-go](https://github.com/gagliardetto/solana-go) - Solana Go SDK
- [GORM](https://gorm.io) - Go ORM library
