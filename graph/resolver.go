package graph

// This file will not be regenerated automatically.
//
// It serves as dependency injection for your app, add any dependencies you require
// here.

import (
	"time"

	"github.com/hcchien/nl/ent"
)

const sessionTTL = 7 * 24 * time.Hour

type Resolver struct {
	Client *ent.Client
	// TokenSecret 用於簽發/驗證 session token
	TokenSecret []byte
}
