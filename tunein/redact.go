package tunein

import "regexp"

var tokenParamRe = regexp.MustCompile(
	`(?i)([?&;](?:accesskey|access_token|token|auth|authtoken|sig|signature|policy|key-pair-id|hdnts|hdnea|x-amz-signature|x-amz-credential|x-amz-security-token)=)[^&\s"'<>]*`)

// Redact masks access tokens in URLs (or in any text containing them) so
// they can be logged: "?accessKey=abc" becomes "?accessKey=[REDACTED]".
func Redact(s string) string { return tokenParamRe.ReplaceAllString(s, "${1}[REDACTED]") }

// HasToken reports whether s contains a token parameter Redact would mask.
func HasToken(s string) bool { return tokenParamRe.MatchString(s) }
