package queue

import (
	"github.com/ThreeDotsLabs/watermill"
	"github.com/rs/zerolog/log"
)

// Logger represents logger data.
type Logger struct {
	fields watermill.LogFields
	Tag    string
}

// NewLogger creates a new logger.
func NewLogger() *Logger {
	return &Logger{
		Tag: "watermill",
	}
}

func (l Logger) Error(msg string, err error, fields watermill.LogFields) {
	log.Error().Err(err).Str("tag", l.Tag).Fields(l.fields.Add(fields)).Msg(msg)
}

// Info performs the info operation.
func (l Logger) Info(msg string, fields watermill.LogFields) {
	log.Info().Str("tag", l.Tag).Fields(l.fields.Add(fields)).Msg(msg)
}

// Debug performs the debug operation.
func (l Logger) Debug(msg string, fields watermill.LogFields) {
	log.Debug().Str("tag", l.Tag).Fields(l.fields.Add(fields)).Msg(msg)
}

// Trace performs the trace operation.
func (l Logger) Trace(msg string, fields watermill.LogFields) {
	log.Trace().Str("tag", l.Tag).Fields(l.fields.Add(fields)).Msg(msg)
}

// With performs the with operation.
func (l Logger) With(fields watermill.LogFields) watermill.LoggerAdapter {
	return &Logger{
		Tag:    l.Tag,
		fields: l.fields.Add(fields),
	}
}
