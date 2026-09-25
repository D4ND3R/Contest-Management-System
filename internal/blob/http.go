package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HTTP is a store served by "cms blob-server" (the main server's blob store
// seen from a worker on another machine). Reads are checked against their
// digest; deleting is not allowed.
type HTTP struct {
	base  string
	token string
	c     *http.Client
}

// NewHTTP returns a client for the blob server at url.
func NewHTTP(url, token string) *HTTP {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 16
	return &HTTP{base: strings.TrimRight(url, "/") + "/v1/blobs", token: token,
		c: &http.Client{Transport: tr, Timeout: 30 * time.Minute}}
}

func (h *HTTP) request(ctx context.Context, method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := h.c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("blob server: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return resp, nil
	case http.StatusNotFound:
		resp.Body.Close()
		return nil, ErrNotFound
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	resp.Body.Close()
	return nil, fmt.Errorf("blob server: %s %s", resp.Status, bytes.TrimSpace(msg))
}

type putReply struct {
	Digest  string `json:"digest"`
	Size    int64  `json:"size"`
	Created bool   `json:"created"`
}

func (h *HTTP) Put(ctx context.Context, r io.Reader) (Info, error) {
	// The server names the content; the client checks it named it right.
	hs := sha256.New()
	resp, err := h.request(ctx, http.MethodPost, h.base, io.TeeReader(r, hs))
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	var p putReply
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&p); err != nil {
		return Info{}, fmt.Errorf("blob server reply: %w", err)
	}
	if p.Digest != hex.EncodeToString(hs.Sum(nil)) {
		return Info{}, ErrDigestMismatch
	}
	return Info{Digest: p.Digest, Size: p.Size, Created: p.Created}, nil
}

func (h *HTTP) PutBytes(ctx context.Context, b []byte) (Info, error) {
	return h.Put(ctx, bytes.NewReader(b))
}

func (h *HTTP) Open(ctx context.Context, digest string) (io.ReadCloser, error) {
	if err := checkDigest(digest); err != nil {
		return nil, err
	}
	resp, err := h.request(ctx, http.MethodGet, h.base+"/"+digest, nil)
	if err != nil {
		return nil, err
	}
	return &verifyingReader{rc: resp.Body, h: sha256.New(), digest: digest}, nil
}

func (h *HTTP) Stat(ctx context.Context, digest string) (int64, error) {
	if err := checkDigest(digest); err != nil {
		return 0, err
	}
	resp, err := h.request(ctx, http.MethodHead, h.base+"/"+digest, nil)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
}

// ErrReadOnly is returned by operations a remote store does not allow.
var ErrReadOnly = errors.New("blob deletion is not allowed through the blob server")

func (h *HTTP) Delete(ctx context.Context, digest string) error { return ErrReadOnly }

// verifyingReader fails the final read when the content does not match its
// digest (a corrupted transfer or store).
type verifyingReader struct {
	rc     io.ReadCloser
	h      hash.Hash
	digest string
}

func (v *verifyingReader) Read(p []byte) (int, error) {
	n, err := v.rc.Read(p)
	v.h.Write(p[:n])
	if err == io.EOF && hex.EncodeToString(v.h.Sum(nil)) != v.digest {
		return n, ErrDigestMismatch
	}
	return n, err
}

func (v *verifyingReader) Close() error { return v.rc.Close() }
