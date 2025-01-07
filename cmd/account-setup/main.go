package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/imax9000/errors"
	"gopkg.in/yaml.v3"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/xrpc"

	"bsky.watch/labeler/account"
	"bsky.watch/labeler/sign"
	"bsky.watch/utils/xrpcauth"

	"bsky.watch/modkit/pkg/config"
)

var (
	configFile = flag.String("config", "./config/config.yaml", "Path to the config file")
	token      = flag.String("token", "", "Token that PDS requires to sign PLC operations")
)

func runMain(ctx context.Context) error {
	b, err := os.ReadFile(*configFile)
	if err != nil {
		return fmt.Errorf("reading config file: %w", err)
	}

	config := &config.Config{}
	if err := yaml.Unmarshal(b, config); err != nil {
		return fmt.Errorf("parsing config file: %w", err)
	}

	if config.ModerationAccount.Password == "" {
		return fmt.Errorf("password is not specified in the config")
	}

	publicUrl, err := url.Parse(fmt.Sprintf("https://%s", config.PublicHostname))
	if err != nil {
		return fmt.Errorf("missing/bad public hostname")
	}

	key, err := sign.ParsePrivateKey(config.LabelSigningKey)
	if err != nil {
		return fmt.Errorf("parsing private key: %w", err)
	}
	publicKey, err := sign.GetPublicKey(key)
	if err != nil {
		return fmt.Errorf("failed to get the public key: %w", err)
	}

	client := xrpcauth.NewClientWithTokenSource(ctx, xrpcauth.PasswordAuth(config.ModerationAccount.DID, config.ModerationAccount.Password))

	fmt.Println("Checking labeler record...")
	_, err = atproto.RepoGetRecord(ctx, client, "", "app.bsky.labeler.service", config.ModerationAccount.DID, "self")
	missingRecord := false
	if err != nil {
		if err, ok := errors.As[*xrpc.XRPCError](err); ok {
			if strings.HasPrefix(err.Message, "Could not locate record: ") {
				missingRecord = true
			}
		}
		if !missingRecord {
			return fmt.Errorf("com.atproto.repo.getRecord: %w", err)
		}
	}
	if missingRecord {
		fmt.Println("Creating labeler record...")
		err := account.UpdateLabelDefs(ctx, client, &bsky.LabelerDefs_LabelerPolicies{})
		if err != nil {
			return fmt.Errorf("creating labeler record: %w", err)
		}
	}
	fmt.Println("Labeler record OK.")

	fmt.Println("Checking DID document fields...")
	err = account.UpdateSigningKeyAndEndpoint(ctx, client, *token, publicKey, publicUrl.String())
	if err != nil {
		if *token == "" {
			fmt.Fprintln(os.Stderr, "If you need to provide a token, re-run this command with --token=YOUR-TOKEN flag")
		}
		return err
	}
	fmt.Println("DID document OK.")

	return nil
}

func main() {
	flag.Parse()

	if err := runMain(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}
