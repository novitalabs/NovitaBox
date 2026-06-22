package sqlite

import "time"

func unixNow() int64 {
	return time.Now().UTC().Unix()
}

func unixTime(seconds int64) time.Time {
	return time.Unix(seconds, 0).UTC()
}
