package credit

// Asynq names of the credit queue.
const (
	// TaskTypeNotification replays a store webhook that failed to process.
	TaskTypeNotification = "kredi:bildirim"
	// QueueCredit is the asynq queue of the credit tasks.
	QueueCredit = "kredi"
)
