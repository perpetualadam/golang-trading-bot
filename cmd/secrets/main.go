// Command secrets encrypts or decrypts strings for api_key_enc / api_secret_enc / passphrase_enc
// using the same AES-GCM format as cmd/bot. Run offline on a trusted machine.
//
//	TRADING_MASTER_KEY=... go run ./cmd/secrets encrypt "plaintext"
//	echo plaintext | TRADING_MASTER_KEY=... go run ./cmd/secrets encrypt
//	TRADING_MASTER_KEY=... go run ./cmd/secrets decrypt "base64..."
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/subosito/gotenv"
	"tradingbot/internal/secrets"
)

func main() {
	_ = gotenv.Load()

	if len(os.Args) < 2 {
		usage()
	}
	cmd := strings.ToLower(strings.TrimSpace(os.Args[1]))
	switch cmd {
	case "encrypt", "decrypt":
		break
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
	}

	val := readValue(os.Args[2:])
	if val == "" {
		fmt.Fprintln(os.Stderr, "missing value: pass as arguments or pipe one line on stdin")
		os.Exit(2)
	}

	key, err := secrets.DeriveKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	switch cmd {
	case "encrypt":
		out, err := secrets.EncryptString(val, key)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(out)
	case "decrypt":
		out, err := secrets.DecryptString(val, key)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(out)
	}
}

func readValue(args []string) string {
	if len(args) > 0 {
		return strings.Join(args, " ")
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSpace(line)
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  TRADING_MASTER_KEY=... %s encrypt [plaintext...]   # omit args to read one line from stdin
  TRADING_MASTER_KEY=... %s decrypt [ciphertext...]

Plaintext with spaces: use stdin or quote the whole secret.
Ciphertext is base64 (nonce||ciphertext), same format the bot decrypts at startup.
`, os.Args[0], os.Args[0])
	os.Exit(2)
}
