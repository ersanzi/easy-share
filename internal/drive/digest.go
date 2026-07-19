package drive

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

const webDAVRealm = "EasyShare"

type digestAuthenticator struct {
	username string
	password string
	nonce    string
}

func newDigestAuthenticator(username, password string) (*digestAuthenticator, error) {
	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		return nil, fmt.Errorf("generate WebDAV digest nonce: %w", err)
	}
	return &digestAuthenticator{username: username, password: password, nonce: hex.EncodeToString(nonceBytes)}, nil
}

func (auth *digestAuthenticator) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !auth.authorized(request) {
			writer.Header().Set("WWW-Authenticate", fmt.Sprintf(`Digest realm=%q, nonce=%q, algorithm=MD5, qop="auth"`, webDAVRealm, auth.nonce))
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (auth *digestAuthenticator) authorized(request *http.Request) bool {
	scheme, parameters, ok := parseDigestHeader(request.Header.Get("Authorization"))
	if !ok || !strings.EqualFold(scheme, "Digest") {
		return false
	}
	if !secureEqual(parameters["username"], auth.username) ||
		!secureEqual(parameters["realm"], webDAVRealm) ||
		!secureEqual(parameters["nonce"], auth.nonce) {
		return false
	}
	algorithm := parameters["algorithm"]
	if algorithm != "" && !strings.EqualFold(algorithm, "MD5") {
		return false
	}
	uri := parameters["uri"]
	if uri == "" || uri != request.URL.RequestURI() {
		return false
	}

	ha1 := md5Hex(auth.username + ":" + webDAVRealm + ":" + auth.password)
	ha2 := md5Hex(request.Method + ":" + uri)
	var expected string
	qop := parameters["qop"]
	switch {
	case strings.EqualFold(qop, "auth"):
		nonceCount, clientNonce := parameters["nc"], parameters["cnonce"]
		if len(nonceCount) != 8 || clientNonce == "" {
			return false
		}
		expected = md5Hex(ha1 + ":" + auth.nonce + ":" + nonceCount + ":" + clientNonce + ":auth:" + ha2)
	case qop == "":
		// RFC 2069 compatibility for older WebDAV redirectors.
		expected = md5Hex(ha1 + ":" + auth.nonce + ":" + ha2)
	default:
		return false
	}
	return secureEqual(strings.ToLower(parameters["response"]), expected)
}

func md5Hex(value string) string {
	sum := md5.Sum([]byte(value)) // #nosec G501 -- HTTP Digest requires MD5 for Windows WebClient compatibility.
	return hex.EncodeToString(sum[:])
}

func secureEqual(actual, expected string) bool {
	if len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

// parseDigestHeader parses both a Digest Authorization header and the
// comma-separated parameters from a WWW-Authenticate challenge.
func parseDigestHeader(value string) (string, map[string]string, bool) {
	value = strings.TrimSpace(value)
	space := strings.IndexByte(value, ' ')
	if space <= 0 {
		return "", nil, false
	}
	scheme := value[:space]
	input := strings.TrimSpace(value[space+1:])
	parameters := make(map[string]string)
	for input != "" {
		input = strings.TrimLeft(input, " ,\t")
		if input == "" {
			break
		}
		equals := strings.IndexByte(input, '=')
		if equals <= 0 {
			return "", nil, false
		}
		key := strings.ToLower(strings.TrimSpace(input[:equals]))
		input = strings.TrimLeft(input[equals+1:], " \t")
		if key == "" || input == "" {
			return "", nil, false
		}
		var parsed string
		if input[0] == '"' {
			input = input[1:]
			var builder strings.Builder
			closed := false
			for len(input) > 0 {
				character := input[0]
				input = input[1:]
				if character == '"' {
					closed = true
					break
				}
				if character == '\\' && len(input) > 0 {
					character, input = input[0], input[1:]
				}
				builder.WriteByte(character)
			}
			if !closed {
				return "", nil, false
			}
			parsed = builder.String()
		} else {
			comma := strings.IndexByte(input, ',')
			if comma < 0 {
				parsed, input = strings.TrimSpace(input), ""
			} else {
				parsed, input = strings.TrimSpace(input[:comma]), input[comma:]
			}
		}
		parameters[key] = parsed
		input = strings.TrimLeft(input, " \t")
		if strings.HasPrefix(input, ",") {
			input = input[1:]
		} else if input != "" {
			return "", nil, false
		}
	}
	if len(parameters) == 0 {
		return "", nil, false
	}
	return scheme, parameters, true
}
