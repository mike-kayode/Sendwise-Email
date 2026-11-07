package delivery

import (
	"errors"
	"net/mail"
	"strings"
)

func NormalizeSenderAddress(raw string) (string, error) {
	address, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil || address.Name != "" {
		return "", errors.New("sender must be a valid email address without a display name")
	}
	at := strings.LastIndexByte(address.Address, '@')
	if at < 1 || at == len(address.Address)-1 {
		return "", errors.New("sender must include a local-part and domain")
	}
	local := address.Address[:at]
	if len(local) > 64 {
		return "", errors.New("sender local-part must be at most 64 bytes")
	}
	for _, char := range local {
		if char > 127 {
			return "", errors.New("sender local-part must use RFC 5322 ASCII characters")
		}
	}
	reserved := strings.ToLower(strings.Trim(local, `"`))
	switch reserved {
	case "postmaster", "abuse", "hostmaster":
		return "", errors.New("sender local-part is reserved")
	}
	return strings.ToLower(address.Address), nil
}
