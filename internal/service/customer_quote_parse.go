package service

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	customerQtyBarRE      = regexp.MustCompile(`(\d+)\s*条`)
	customerQtyPieceRE    = regexp.MustCompile(`(\d+)\s*个`)
	customerNamedYuanRE   = regexp.MustCompile(`(\d+)\s*元`)
	customerNamedWantRE   = regexp.MustCompile(`我想\s*(\d+)`)
	customerBareNumberRE  = regexp.MustCompile(`^\s*(\d+)\s*$`)
	customerBandwidthRE   = regexp.MustCompile(`(\d+)\s*M`)
)

func ParseCustomerQuantity(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	if match := customerBareNumberRE.FindStringSubmatch(text); len(match) == 2 {
		return atoiBounded(match[1], 1, 100000)
	}
	for _, re := range []*regexp.Regexp{customerQtyBarRE, customerQtyPieceRE} {
		for _, loc := range re.FindAllStringSubmatchIndex(text, -1) {
			if len(loc) < 4 {
				continue
			}
			if yuanPrecedes(text, loc[0]) {
				continue
			}
			n := atoiBounded(text[loc[2]:loc[3]], 1, 100000)
			if n > 0 {
				return n
			}
		}
	}
	return 0
}

func ParseCustomerNamedPrice(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	if match := customerNamedYuanRE.FindStringSubmatch(text); len(match) == 2 {
		return atoiBounded(match[1], 1, 999)
	}
	if match := customerNamedWantRE.FindStringSubmatch(text); len(match) == 2 {
		return atoiBounded(match[1], 1, 999)
	}
	return 0
}

func customerLooksDiscountRequest(text string) bool {
	return containsAny(strings.ToLower(text), "便宜", "优惠", "太贵", "降价", "再低", "压价", "少点", "打折")
}

func normalizeCustomerBandwidth(text string) string {
	compact := strings.ToUpper(strings.TrimSpace(text))
	compact = strings.NewReplacer("兆", "M", "Ｍ", "M").Replace(compact)
	if match := customerBandwidthRE.FindStringSubmatch(compact); len(match) == 2 {
		return match[1] + "M"
	}
	return ""
}

func yuanPrecedes(text string, index int) bool {
	if index <= 0 {
		return false
	}
	prefix := []rune(text[:index])
	for i := len(prefix) - 1; i >= 0; i-- {
		if unicode.IsSpace(prefix[i]) {
			continue
		}
		return prefix[i] == '元'
	}
	return false
}

func atoiBounded(text string, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < min || n > max {
		return 0
	}
	return n
}
