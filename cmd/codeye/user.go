package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/account"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// Account commands of the operator CLI. Passwords are never taken from argv
// or the environment: they come from --password-file (a regular file not
// accessible by group or others) or standard input (one line; a terminal is
// read with echo off).

// passwordStdin is the standard input passwords are read from (tests
// replace it with a pipe).
var passwordStdin = os.Stdin

const maxPasswordInput = 4096

// readPassword returns the password from file, or from standard input.
func readPassword(file string, stderr io.Writer, confirm bool) (string, error) {
	if file != "" {
		st, err := os.Stat(file)
		if err != nil || !st.Mode().IsRegular() {
			return "", errors.New("--password-file is not a readable regular file")
		}
		if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("--password-file mode %04o is accessible by group or others; use 0600", st.Mode().Perm())
		}
		raw, err := os.ReadFile(file)
		if err != nil || len(raw) > maxPasswordInput {
			return "", errors.New("--password-file cannot be read")
		}
		return firstLine(string(raw)), nil
	}
	if isTerminal(passwordStdin) {
		pw, err := readNoEcho(passwordStdin, stderr, "Password: ")
		if err != nil {
			return "", err
		}
		if confirm {
			again, err := readNoEcho(passwordStdin, stderr, "Repeat password: ")
			if err != nil {
				return "", err
			}
			if again != pw {
				return "", errors.New("the passwords do not match")
			}
		}
		return pw, nil
	}
	line, err := bufio.NewReaderSize(io.LimitReader(passwordStdin, maxPasswordInput), maxPasswordInput).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("cannot read the password from standard input")
	}
	return firstLine(line), nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return strings.TrimSuffix(s, "\r")
}

// derivePassword checks the policy and derives the verifier. The error text
// never contains the password.
func derivePassword(pw, username, display string) (account.Hash, error) {
	if err := account.CheckPassword(pw, username, display); err != nil {
		return account.Hash{}, errors.New("the password does not meet the policy: 12 to 256 characters, not the username or display name, not one repeated character")
	}
	return account.Derive(pw, account.DefaultIterations)
}

func userCreate(ctx context.Context, args []string, out, stderr io.Writer) int {
	var name, username, pwFile string
	var admin bool
	if !parse("user create", args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "name", "", "")
		fs.StringVar(&username, "username", "", "")
		fs.StringVar(&pwFile, "password-file", "", "")
		fs.BoolVar(&admin, "admin", false, "")
	}) {
		return 2
	}
	in := postgres.NewAccount{DisplayName: name, IsAdmin: admin}
	if username != "" {
		n, err := account.NormalizeUsername(username)
		if err != nil {
			fmt.Fprintln(stderr, "--username must be 2..64 characters of a-z, 0-9, '.', '_' or '-', starting with a letter or digit")
			return 2
		}
		pw, err := readPassword(pwFile, stderr, true)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		h, err := derivePassword(pw, n, name)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		in.Username, in.Password = n, &h
	} else if pwFile != "" {
		fmt.Fprintln(stderr, "--password-file needs --username")
		return 2
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		u, err := s.CreateAccount(ctx, postgres.OperatorPrincipal(), in)
		if err != nil {
			if errors.Is(err, domain.ErrConflict) {
				fmt.Fprintln(stderr, "cannot create user: the username is taken")
			} else {
				fmt.Fprintln(stderr, "cannot create user:", err)
			}
			return 1
		}
		res := map[string]string{"id": u.ID}
		if u.Username != nil {
			res["username"] = *u.Username
		}
		printJSON(out, res)
		return 0
	})
}

// userFlags defines --id and --username (exactly one names the user).
func userFlags(fs *flag.FlagSet, id, username *string) {
	fs.StringVar(id, "id", "", "")
	fs.StringVar(username, "username", "", "")
}

func findUser(ctx context.Context, s *postgres.Store, id, username string, stderr io.Writer) (postgres.User, bool) {
	key := id
	if key == "" {
		key = username
	}
	u, err := s.FindUser(ctx, key)
	if err != nil {
		fmt.Fprintln(stderr, "unknown user")
		return u, false
	}
	return u, true
}

func oneUser(id, username string, stderr io.Writer) bool {
	if (id == "") == (username == "") {
		fmt.Fprintln(stderr, "exactly one of --id or --username is required")
		return false
	}
	return true
}

func userDisable(ctx context.Context, args []string, out, stderr io.Writer) int {
	var id, username string
	if !parse("user disable", args, stderr, func(fs *flag.FlagSet) { userFlags(fs, &id, &username) }) || !oneUser(id, username, stderr) {
		return 2
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		u, ok := findUser(ctx, s, id, username, stderr)
		if !ok {
			return 1
		}
		if _, err := s.DisableAccount(ctx, postgres.OperatorPrincipal(), u.ID); err != nil {
			if errors.Is(err, domain.ErrConflict) {
				fmt.Fprintln(stderr, "cannot disable the last enabled administrator; make another user an administrator first")
			} else {
				fmt.Fprintln(stderr, "cannot disable user:", err)
			}
			return 1
		}
		fmt.Fprintln(out, "user disabled; its sessions are revoked")
		return 0
	})
}

func userAdmin(ctx context.Context, args []string, out, stderr io.Writer) int {
	var id, username string
	var set, unset bool
	if !parse("user admin", args, stderr, func(fs *flag.FlagSet) {
		userFlags(fs, &id, &username)
		fs.BoolVar(&set, "set", false, "")
		fs.BoolVar(&unset, "unset", false, "")
	}) || !oneUser(id, username, stderr) {
		return 2
	}
	if set == unset {
		fmt.Fprintln(stderr, "exactly one of --set or --unset is required")
		return 2
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		u, ok := findUser(ctx, s, id, username, stderr)
		if !ok {
			return 1
		}
		if _, err := s.SetAdmin(ctx, postgres.OperatorPrincipal(), u.ID, set); err != nil {
			if errors.Is(err, domain.ErrConflict) {
				fmt.Fprintln(stderr, "cannot change the administrator role: the user is disabled, or it is the last enabled administrator")
			} else {
				fmt.Fprintln(stderr, "cannot change the administrator role:", err)
			}
			return 1
		}
		if set {
			fmt.Fprintln(out, "user is an administrator (account and access management; project access still needs membership)")
		} else {
			fmt.Fprintln(out, "user is no longer an administrator")
		}
		return 0
	})
}

func userPasswd(ctx context.Context, args []string, out, stderr io.Writer) int {
	var id, username, pwFile string
	var mustChange bool
	if !parse("user passwd", args, stderr, func(fs *flag.FlagSet) {
		userFlags(fs, &id, &username)
		fs.StringVar(&pwFile, "password-file", "", "")
		fs.BoolVar(&mustChange, "must-change", false, "")
	}) || !oneUser(id, username, stderr) {
		return 2
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		u, ok := findUser(ctx, s, id, username, stderr)
		if !ok {
			return 1
		}
		identifier := u.Email
		if u.Username != nil {
			identifier = u.Username
		}
		if identifier == nil || u.DisabledAt != nil {
			fmt.Fprintln(stderr, "only an enabled user with a login identifier can have a password")
			return 1
		}
		pw, err := readPassword(pwFile, stderr, true)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		display := ""
		if u.DisplayName != nil {
			display = *u.DisplayName
		}
		h, err := derivePassword(pw, *identifier, display)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		if _, err := s.SetPassword(ctx, u.ID, h, mustChange); err != nil {
			fmt.Fprintln(stderr, "cannot set the password:", err)
			return 1
		}
		fmt.Fprintln(out, "password set; the user's sessions are revoked")
		return 0
	})
}

func userList(ctx context.Context, args []string, out, stderr io.Writer) int {
	if !parse("user list", args, stderr, func(*flag.FlagSet) {}) {
		return 2
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		users, err := s.OperatorUsers(ctx, 200)
		if err != nil {
			fmt.Fprintln(stderr, "cannot list users:", err)
			return 1
		}
		type item struct {
			ID                 string  `json:"id"`
			Username           *string `json:"username"`
			Email              *string `json:"email,omitempty"`
			DisplayName        *string `json:"display_name"`
			IsAdmin            bool    `json:"is_admin"`
			HasPassword        bool    `json:"has_password"`
			MustChangePassword bool    `json:"must_change_password"`
			Disabled           bool    `json:"disabled"`
			LastLoginAt        *string `json:"last_login_at"`
		}
		items := make([]item, 0, len(users))
		for _, u := range users {
			it := item{ID: u.ID, Username: u.Username, Email: u.Email, DisplayName: u.DisplayName, IsAdmin: u.IsAdmin, HasPassword: u.HasPassword,
				MustChangePassword: u.MustChangePassword, Disabled: u.DisabledAt != nil}
			if u.LastLoginAt != nil {
				t := u.LastLoginAt.UTC().Format(time.RFC3339)
				it.LastLoginAt = &t
			}
			items = append(items, it)
		}
		printJSON(out, map[string]any{"items": items})
		return 0
	})
}
