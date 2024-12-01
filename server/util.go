package main

import "time"

func GetUnixTimestampMS() uint64 {
	return uint64(time.Now().UnixMilli())
}
