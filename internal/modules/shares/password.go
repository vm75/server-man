package shares

import (
	"context"
	"io"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"

	"server-man/internal/core"
)

// CurrentAccount resolves the process account without trusting USER or HOME.
func CurrentAccount() (string, error) {
	account, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil || !validAccount(account.Username) {
		return "", core.Unavailable("The server-man Linux account is unavailable or unsupported")
	}
	return account.Username, nil
}

func validAccount(account string) bool {
	return userPattern.MatchString(account) && !strings.HasPrefix(account, "@")
}

func (b *Backend) passwordTool() core.Tool {
	return core.Tool{
		Name: "shares_samba_password", Title: "Set Samba password",
		Description: "Set or replace the Samba password for the Linux account running server-man. Does not change the Linux login password. The password is never stored in configuration or job output.",
		Destructive: true, JobTimeout: time.Minute,
		Fields: map[string]core.Field{"password": core.StringField("Non-empty Samba password (no NUL or line breaks)", true, 256)},
		Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			password := in.String("password")
			if strings.TrimSpace(password) == "" || len(password) > 1024 || strings.ContainsAny(password, "\x00\r\n") {
				return nil, core.Invalid("Samba password must be non-empty, at most 256 characters, and contain no NUL or line breaks")
			}
			if !validAccount(b.account) {
				return nil, core.Unavailable("The server-man Linux account is unavailable or unsupported")
			}
			program := b.programs["smbpasswd"]
			if program == "" {
				return nil, core.Unavailable("smbpasswd is unavailable; set up the Samba server and restart server-man")
			}
			// smbpasswd's silent mode accepts two password lines through stdin. Neither
			// native output nor errors may be retained: they could echo secret input.
			err := b.runner.Run(ctx, core.Command{Program: program, Args: []string{"-s", "-a", b.account}, Stdin: strings.NewReader(password + "\n" + password + "\n"), Timeout: 20 * time.Second}, io.Discard)
			if err != nil {
				return nil, core.Unavailable("Could not set the Samba password; check Samba installation and privileged helper access")
			}
			return map[string]any{"username": b.account, "updated": true}, nil
		},
	}
}
