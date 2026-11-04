package db

import (
	"context"

	"gorm.io/gorm/logger"
)

// imageQueryLogger keeps source URLs and result payloads out of SQL traces.
type imageQueryLogger struct {
	logger.Interface
}

func (l imageQueryLogger) LogMode(level logger.LogLevel) logger.Interface {
	return imageQueryLogger{Interface: l.Interface.LogMode(level)}
}

func (imageQueryLogger) ParamsFilter(_ context.Context, statement string, _ ...any) (string, []any) {
	return statement, nil
}
