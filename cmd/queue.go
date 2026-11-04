package cmd

import (
	"errors"

	"github.com/rs/zerolog/log"

	"github.com/caitun/inferkit/queue"
	"github.com/caitun/inferkit/redis"

	"github.com/spf13/cobra"
)

var subscriberID string

// ErrQueueCommand indicates run queue command failed.
var ErrQueueCommand = errors.New("run queue command failed")

// queueCmd represents the queue command
var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "A command of queue listener to process jobs",
	Long:  "Start this command to process jobs in the queues.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		err := redis.Init(cmd.Context())
		if err != nil {
			log.Error().Err(err).Msg("connect redis failed")
			return errors.Join(ErrQueueCommand, err)
		}
		err = queue.Init()
		if err != nil {
			log.Error().Err(err).Msg("init queue publisher failed with error")
			return errors.Join(ErrQueueCommand, err)
		}
		err = queue.Start(cmd.Context(), subscriberID)
		if err != nil {
			log.Error().Err(err).Msg("start queue processes failed with error")
			return errors.Join(ErrQueueCommand, err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(queueCmd)
	queueCmd.Flags().StringVarP(&subscriberID, "subscriber", "s", "", "The subscriber id")
}
