package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/server"
	"github.com/spf13/cobra"
)

func recoveryDelegateCmd() *cobra.Command {
	var to string
	var disable bool
	c := &cobra.Command{Use: "delegate", Short: "Allow an operator to recover this vault with their public key, or withdraw that access", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		if to == "" && !disable || to != "" && disable {
			return errors.New("choose --to <operator-public-key> or --disable")
		}
		e, err := engine()
		if err != nil {
			return err
		}
		if to != "" {
			fmt.Fprintln(os.Stderr, "The holder of the matching private key with access to the recovery envelope can unlock this account's backups. Verify the public key with your operator.")
		}
		pw, err := readPassword("Current master password: ", false)
		if err != nil {
			return err
		}
		defer clear(pw)
		return report(e.SetRecoveryDelegate(pw, to), "recovery delegation updated")
	}}
	c.Flags().StringVar(&to, "to", "", "operator's X25519 public key")
	c.Flags().BoolVar(&disable, "disable", false, "withdraw delegation and outstanding codes")
	return c
}

func resetOperatorKeyCmd() *cobra.Command {
	return &cobra.Command{Use: "recovery-keygen <new-private-key-file>", Short: "Create an offline operator recovery key and print the public key for users", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		key, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		raw := key.Bytes()
		defer clear(raw)
		f, err := os.OpenFile(args[0], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if _, err = f.Write(raw); err != nil {
			f.Close()
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		fmt.Println(base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()))
		return nil
	}}
}

func issueResetCodeCmd(with withFn) *cobra.Command {
	var path string
	c := &cobra.Command{Use: "reset-password <email>", Short: "Issue a 30-minute bearer reset code for an enrolled account", Args: cobra.ExactArgs(1), RunE: with(func(_ *cobra.Command, s *server.Server, args []string) error {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != 32 {
			return errors.New("use the private 32-byte operator key file (0600)")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		defer clear(raw)
		key, err := ecdh.X25519().NewPrivateKey(raw)
		if err != nil {
			return errors.New("invalid operator key")
		}
		code, err := s.IssueResetCode(args[0], key)
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Deliver privately. Anyone with this code and the account email can set its password within 30 minutes.")
		fmt.Println(code)
		return nil
	})}
	c.Flags().StringVar(&path, "key-file", "", "offline private key file from recovery-keygen")
	c.MarkFlagRequired("key-file")
	return c
}

func resetWithCode(server, email string) error {
	code, err := readPasswordVar("HEARTH_RESET_CODE", "Administrator reset code: ", false)
	if err != nil {
		return err
	}
	defer clear(code)
	pw, err := readPasswordVar("HEARTH_NEW_PASSWORD", "New password: ", true)
	if err != nil {
		return err
	}
	defer clear(pw)
	return report(client.ResetPasswordWithCode(client.Home(), server, email, strings.TrimSpace(string(code)), pw), "Password reset. Use the new password with hearth login.")
}
