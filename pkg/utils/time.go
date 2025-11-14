package utils

import "time"

func StartOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func TruncateToMinutes(t time.Time) time.Time {
	return t.Truncate(time.Minute)
}

func TimesEqualUpToMinutes(t1, t2 time.Time) bool {
	t1Truncated := TruncateToMinutes(t1)
	t2Truncated := TruncateToMinutes(t2)
	return t1Truncated.Equal(t2Truncated)
}

func DatesEqual(t1, t2 time.Time) bool {
	return StartOfDay(t1).Equal(StartOfDay(t2))
}
