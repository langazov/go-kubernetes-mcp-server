package config

import (
	"os"
	"strconv"
	"strings"
)

func envKey(flag string) string {
	return "K8S_MCP_" + strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
}

func lookupEnv(key string) (string, bool) { return os.LookupEnv(key) }

func parseInt(s string) (int, error) { return strconv.Atoi(s) }

func parseFloat(s string) (float64, error) { return strconv.ParseFloat(s, 64) }
