package credit

// Asynq names of the credit queue.
const (
	// TaskTypeNotification replays a store webhook that failed to process.
	TaskTypeNotification = "kredi:bildirim"
	// TaskTypeRefill runs the yearly refill, the resync and the pruning.
	TaskTypeRefill = "kredi:dolum"
	// TaskTypeExpire sweeps expired lots, ends campaigns by date and retries
	// missing signup gifts.
	TaskTypeExpire = "kredi:sure"
	// QueueCredit is the asynq queue of the credit tasks.
	QueueCredit = "kredi"
)
