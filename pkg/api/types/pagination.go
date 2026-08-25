package types

// CoinPageResult is one page from a complete coin-listing stream. Err is set
// instead of silently terminating the stream when any page cannot be fetched.
type CoinPageResult struct {
	Coins []Coin
	Err   error
}
