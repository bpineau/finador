package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// ParsePercent reads "15%" (or "15") into 0.15, bounded to [0%, 100%].
func ParsePercent(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
	if err != nil || v < 0 || v > 100 {
		return 0, fmt.Errorf("pourcentage %q invalide (attendu 0%% à 100%%)", s)
	}
	return v / 100, nil
}
