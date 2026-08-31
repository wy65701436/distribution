package handlers

import (
	"errors"
	"net/http"
	"testing"

	"github.com/docker/distribution/registry/api/errcode"
	v2 "github.com/docker/distribution/registry/api/v2"
)

func TestToErrcodeErrors(t *testing.T) {
	for _, tc := range []struct {
		name           string
		err            error
		wantLen        int
		wantCode       errcode.ErrorCode
		wantHTTPStatus int
	}{
		{
			name:    "nil error returns nil",
			err:     nil,
			wantLen: 0,
		},
		{
			name:           "upstream errcode.Errors are preserved",
			err:            errcode.Errors{errcode.ErrorCodeDenied.WithMessage("requested access to the resource is denied"), errcode.ErrorCodeUnauthorized.WithMessage("authentication required")},
			wantLen:        2,
			wantCode:       errcode.ErrorCodeDenied,
			wantHTTPStatus: http.StatusForbidden,
		},
		{
			name:           "single coded error is preserved",
			err:            errcode.ErrorCodeUnauthorized.WithMessage("authentication required"),
			wantLen:        1,
			wantCode:       errcode.ErrorCodeUnauthorized,
			wantHTTPStatus: http.StatusUnauthorized,
		},
		{
			name:           "errcode.ErrorCode is preserved",
			err:            v2.ErrorCodeManifestUnknown,
			wantLen:        1,
			wantCode:       v2.ErrorCodeManifestUnknown,
			wantHTTPStatus: http.StatusNotFound,
		},
		{
			name:           "uncoded error becomes unknown",
			err:            errors.New("database connection failed"),
			wantLen:        1,
			wantCode:       errcode.ErrorCodeUnknown,
			wantHTTPStatus: http.StatusInternalServerError,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := toErrcodeErrors(tc.err)
			if len(got) != tc.wantLen {
				t.Fatalf("toErrcodeErrors(%v) returned %d errors, want %d", tc.err, len(got), tc.wantLen)
			}
			if tc.wantLen == 0 {
				return
			}
			coder, ok := got[0].(errcode.ErrorCoder)
			if !ok {
				t.Fatalf("first error %#v does not implement errcode.ErrorCoder", got[0])
			}
			if coder.ErrorCode() != tc.wantCode {
				t.Errorf("code = %v, want %v", coder.ErrorCode(), tc.wantCode)
			}
			if sc := coder.ErrorCode().Descriptor().HTTPStatusCode; sc != tc.wantHTTPStatus {
				t.Errorf("HTTP status = %d, want %d", sc, tc.wantHTTPStatus)
			}
		})
	}
}
