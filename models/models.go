package models

import "time"

// Trade represents a swap transaction on Solana
type Trade struct {
	ID              uint      `gorm:"primarykey" json:"id"`
	TxID            string    `json:"tx_id" gorm:"uniqueIndex;size:88;not null"`
	Type            string    `json:"type" gorm:"size:4;index;not null"`
	DexProvider     string    `json:"dex_provider" gorm:"size:50;index"`
	Timestamp       int64     `json:"timestamp" gorm:"index"`
	WalletAddress   string    `json:"wallet_address" gorm:"size:44;index;not null"`
	TokenInAddress  string    `json:"token_in_address" gorm:"size:44;index"`
	TokenOutAddress string    `json:"token_out_address" gorm:"size:44;index"`
	TokenInAmount   string    `json:"token_in_amount" gorm:"size:50"`
	TokenOutAmount  string    `json:"token_out_amount" gorm:"size:50"`
	CreatedAt       time.Time `json:"created_at" gorm:"autoCreateTime"`
}

// TableName returns the table name for Trade
func (Trade) TableName() string {
	return "trades"
}

// Wallets represents monitored Solana wallet addresses
type Wallets struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Address   string    `json:"address" gorm:"uniqueIndex;size:44;not null"`
	Label     string    `json:"label" gorm:"size:100"`
	Active    bool      `json:"active" gorm:"default:true;index"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// TableName returns the table name for Wallets
func (Wallets) TableName() string {
	return "wallets"
}
