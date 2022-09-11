package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Transaction struct {
	ID                   primitive.ObjectID `bson:"_id"`
	Sender_Acct_Number   *int               `json:"sender_account_number" validate:"required"`
	Receiver_Acct_Number *int               `json:"receiver_account_number"`
	Amount               *int               `json:"amount"`
	Success              bool               `json:"transfer"`
	Created_at           time.Time          `json:"created_at"`
	Updated_at           time.Time          `json:"updated_at"`
	Transaction_ID       string             `json:"transaction_id"`
}