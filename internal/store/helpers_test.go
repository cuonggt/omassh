package store

import "time"

func timeout() <-chan time.Time { return time.After(5 * time.Second) }
