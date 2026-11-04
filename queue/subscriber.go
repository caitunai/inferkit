package queue

import "github.com/caitun/inferkit/queue/job"

// SubscribeJob performs the subscribe job operation.
func SubscribeJob() {
	register(&job.Example{})
}
