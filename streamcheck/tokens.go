package streamcheck

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Query parameters CDNs use for signed, expiring links (Akamai hdnts/hdnea,
// CloudFront Policy/Signature, S3 X-Amz-*, generic token/expires). A station
// added with one of these plays for minutes or hours and is then dead for
// everyone else.
var tokenParamNames = map[string]bool{
	"token": true, "access_token": true, "auth": true, "authtoken": true, "auth_token": true,
	"sig": true, "signature": true, "hmac": true, "hash": true, "jwt": true,
	"expires": true, "expire": true, "expiry": true, "exp": true, "validto": true, "valid_to": true,
	"session": true, "sessionid": true, "session_id": true, "sid": true, "nonce": true,
	"hdnts": true, "hdnea": true, "__token__": true, "policy": true, "key-pair-id": true,
	"x-amz-signature": true, "x-amz-expires": true, "x-amz-credential": true, "x-amz-security-token": true,
	"lsid": true, "listenerid": true, "aw_0_req_lsid": true,
	"accesskey": true, "access_key": true,
}

// ExpiringParams lists the query parameters of rawURL that look like an
// access token or an expiry time, sorted, or nil.
func ExpiringParams(rawURL string) []string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil
	}
	var out []string
	for k, vals := range u.Query() {
		lk := strings.ToLower(k)
		if tokenParamNames[lk] {
			out = append(out, k)
			continue
		}
		for _, v := range vals {
			if looksLikeExpiry(v) || looksLikeJWT(v) {
				out = append(out, k)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// looksLikeExpiry matches a Unix time from a day ago to a week ahead, the
// usual shape of an expiry under some other name. Signed links often glue it
// to the signature ("1790904816_2e73b3ed..."), so any standalone run of ten
// digits counts, not only a value that is nothing but the number.
func looksLikeExpiry(v string) bool {
	for _, run := range strings.FieldsFunc(v, func(r rune) bool { return r < '0' || r > '9' }) {
		if len(run) != 10 {
			continue
		}
		n, err := strconv.ParseInt(run, 10, 64)
		if err != nil {
			continue
		}
		d := time.Until(time.Unix(n, 0))
		if d > -24*time.Hour && d < 7*24*time.Hour {
			return true
		}
	}
	return false
}

func looksLikeJWT(v string) bool {
	parts := strings.Split(v, ".")
	return len(parts) == 3 && strings.HasPrefix(v, "eyJ") && len(v) > 40
}
