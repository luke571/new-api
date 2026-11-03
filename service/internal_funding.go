package service

import "github.com/QuantumNous/new-api/model"

// InternalFunding retains the actual member on logs and API-key accounting,
// while the transaction chooses the personal or team subscription owner.
type InternalFunding struct {
	requestID string
	userID    int
	consumed  int64
}

func (f *InternalFunding) Source() string { return "internal_subscription" }
func (f *InternalFunding) PreConsume(amount int) error {
	record, err := model.InternalAdjust(f.requestID, f.userID, int64(amount), false)
	if err == nil {
		f.consumed = record.Amount
	}
	return err
}
func (f *InternalFunding) Settle(delta int) error {
	record, err := model.InternalAdjust(f.requestID, f.userID, f.consumed+int64(delta), true)
	if err == nil {
		f.consumed = record.Amount
	}
	return err
}
func (f *InternalFunding) Refund() error {
	return refundWithRetry(func() error { _, err := model.InternalAdjust(f.requestID, f.userID, 0, true); return err })
}
func (f *InternalFunding) reserve(delta int) error {
	record, err := model.InternalAdjust(f.requestID, f.userID, f.consumed+int64(delta), false)
	if err == nil {
		f.consumed = record.Amount
	}
	return err
}
