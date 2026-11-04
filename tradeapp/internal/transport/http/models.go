package httpapi

type fundRequest struct {
	Amount int64 `json:"amount"`
}

type quoteRequest struct {
	Symbol string `json:"symbol"`
	Side   string `json:"side"`
	Qty    int64  `json:"qty"`
}

type placeOrderRequest struct {
	QuoteID string `json:"quote_id"`
}
