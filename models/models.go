package models

type Trade struct {
	ID              uint   `gorm:"primarykey" json:"id"`
	TxID            string `json:"tx_id" gorm:"unique;size:88"`
	Type            string `json:"type" gorm:"size:4"`
	DexProvider     string `json:"dex_provider" gorm:"size:20"`
	Timestamp       int64  `json:"timestamp"`
	WalletAddress   string `json:"wallet_address" gorm:"size:44"`
	TokenInAddress  string `json:"token_in_address" gorm:"size:44"`
	TokenOutAddress string `json:"token_out_address" gorm:"size:44"`
	TokenInAmount   string `json:"token_in_amount" gorm:"size:50"`
	TokenOutAmount  string `json:"token_out_amount" gorm:"size:50"`
}

type Wallets struct {
	ID      uint   `gorm:"primarykey" json:"id"`
	Address string `json:"address" gorm:"unique;size:44"`
}
