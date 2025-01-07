package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	_ "bsky.watch/jwt-go-secp256k1"
	ecrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/golang-jwt/jwt/v5"
	"github.com/multiformats/go-multibase"
	"github.com/multiformats/go-multicodec"

	"bsky.watch/modkit/pkg/resolver"
)

func validateCredentials(ctx context.Context, req *http.Request, acceptedAuds []string) (string, error) {
	header := req.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return "", fmt.Errorf("invalid authorization header")
	}

	token, err := jwt.Parse(strings.TrimPrefix(header, "Bearer "), func(token *jwt.Token) (interface{}, error) {
		issuer, err := token.Claims.GetIssuer()
		if err != nil {
			return nil, fmt.Errorf("missing issuer")
		}

		switch token.Method.Alg() {
		case "ES256":
		case "ES256K":
		default:
			return nil, fmt.Errorf("unexpected alg %q", token.Method.Alg())
		}

		didDoc, err := resolver.GetDocument(ctx, issuer)
		if err != nil {
			return nil, fmt.Errorf("fetching DID document: %w", err)
		}

		keyString := ""
		for _, m := range didDoc.VerificationMethod {
			if !strings.HasSuffix(m.ID, "#atproto") {
				continue
			}
			if m.PublicKeyMultibase == nil {
				continue
			}
			keyString = *m.PublicKeyMultibase
		}
		if keyString == "" {
			return nil, fmt.Errorf("no suitable keys found")
		}

		enc, val, err := multibase.Decode(keyString)
		if err != nil {
			return nil, fmt.Errorf("failed to decode key data: %w", err)
		}

		if enc != multibase.Base58BTC {
			return nil, fmt.Errorf("unexpected key encoding: %v", enc)
		}

		buf := bytes.NewBuffer(val)
		kind, err := binary.ReadUvarint(buf)
		if err != nil {
			return nil, fmt.Errorf("failed to parse key type: %w", err)
		}
		data, _ := io.ReadAll(buf)
		switch multicodec.Code(kind) {
		case multicodec.P256Pub:
			if token.Method.Alg() != "ES256" {
				return nil, fmt.Errorf("signature key doesn't match alg")
			}

			x, y := elliptic.UnmarshalCompressed(elliptic.P256(), data)
			return &ecdsa.PublicKey{
				Curve: elliptic.P256(),
				X:     x,
				Y:     y,
			}, nil
		case multicodec.Secp256k1Pub:
			if token.Method.Alg() != "ES256K" {
				return nil, fmt.Errorf("signature key doesn't match alg")
			}

			return ecrypto.DecompressPubkey(data)
		default:
			return nil, fmt.Errorf("unsupported key type %v", multicodec.Code(kind))
		}
	})
	if err != nil {
		return "", err
	}

	tokAud, err := token.Claims.GetAudience()
	if err != nil {
		return "", err
	}
	accept := false
	for _, aud := range acceptedAuds {
		if slices.Contains(tokAud, aud) {
			accept = true
			break
		}
	}
	if !accept {
		return "", fmt.Errorf("audience of the token does not include us")
	}
	return token.Claims.GetIssuer()
}
