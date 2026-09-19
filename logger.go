package lsmtree

import (
	"fmt"
	"io"
	"log"
	"os"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

type Logger struct {
	level  LogLevel
	debug  *log.Logger
	info   *log.Logger
	warn   *log.Logger
	errLog *log.Logger
}

func NewLogger(level LogLevel, out io.Writer) *Logger {
	return &Logger{
		level:  level,
		debug:  log.New(out, "DEBUG: ", log.Ldate|log.Ltime|log.Lshortfile),
		info:   log.New(out, "INFO:  ", log.Ldate|log.Ltime|log.Lshortfile),
		warn:   log.New(out, "WARN:  ", log.Ldate|log.Ltime|log.Lshortfile),
		errLog: log.New(out, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile),
	}
}

var defaultLogger = NewLogger(INFO, os.Stdout)

func SetLogLevel(level LogLevel) {
	defaultLogger.level = level
}

func SetLogOutput(out io.Writer) {
	defaultLogger = NewLogger(defaultLogger.level, out)
}

func (l *Logger) Debug(format string, v ...any) {
	if l.level <= DEBUG {
		l.debug.Output(2, fmt.Sprintf(format, v...))
	}
}

func (l *Logger) Info(format string, v ...any) {
	if l.level <= INFO {
		l.info.Output(2, fmt.Sprintf(format, v...))
	}
}

func (l *Logger) Warn(format string, v ...any) {
	if l.level <= WARN {
		l.warn.Output(2, fmt.Sprintf(format, v...))
	}
}

func (l *Logger) Error(format string, v ...any) {
	if l.level <= ERROR {
		l.errLog.Output(2, fmt.Sprintf(format, v...))
	}
}
