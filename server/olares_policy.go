package server

import (
	"fmt"
	"slices"
	"strings"

	v1 "github.com/fatedier/frp/pkg/config/v1"
)

func validateOlaresProxyCustomDomains(pxyConf v1.ProxyConfigurer, cfgZones []string, loginUser string) error {
	if len(cfgZones) == 0 {
		return nil
	}
	var cds []string
	switch t := pxyConf.(type) {
	case *v1.HTTPProxyConfig:
		cds = t.CustomDomains
	case *v1.HTTPSProxyConfig:
		cds = t.CustomDomains
	default:
		return nil
	}
	if len(cds) == 0 {
		return nil
	}
	zones := normalizeOlaresPublicZones(cfgZones)
	if len(zones) == 0 {
		return nil
	}
	olaresID, loginDom, ok := parseLoginUser(loginUser)
	if !ok {
		return fmt.Errorf("invalid login user format [%q]", loginUser)
	}
	for _, d := range cds {
		ld := strings.ToLower(strings.TrimSpace(d))
		if err := checkOlaresCustomDomain(ld, olaresID, loginDom, loginUser, zones); err != nil {
			return err
		}
	}
	return nil
}

func normalizeOlaresPublicZones(zones []string) []string {
	out := make([]string, 0, len(zones))
	for _, z := range zones {
		z = strings.ToLower(strings.TrimSpace(z))
		if z != "" {
			out = append(out, z)
		}
	}
	return out
}

func olaresPublicZoneForHost(host string, publicZones []string) (zone string, ok bool) {
	var best string
	for _, dom := range publicZones {
		if strings.HasSuffix(host, "."+dom) && len(dom) > len(best) {
			best = dom
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}

func parseLoginUser(loginUser string) (olaresID, domain string, ok bool) {
	i := strings.IndexByte(loginUser, '@')
	if i <= 0 || i >= len(loginUser)-1 {
		return "", "", false
	}
	return strings.ToLower(loginUser[:i]), strings.ToLower(loginUser[i+1:]), true
}

func hostOlaresIDForZone(hostLower, zone string) (hostOlaresID string, ok bool) {
	suf := "." + zone
	if !strings.HasSuffix(hostLower, suf) {
		return "", false
	}
	remainder := strings.TrimSuffix(hostLower, suf)
	if remainder == "" {
		return "", false
	}
	j := strings.LastIndex(remainder, ".")
	t := remainder[j+1:]
	if t == "" || strings.Contains(t, "*") {
		return "", false
	}
	return strings.ToLower(t), true
}

func sameOlaresClusterSimulated(olaresIDA, olaresIDB string) bool {
	_ = olaresIDA
	_ = olaresIDB
	return false
}

func olaresCustomHostAllowed(ld, olaresID, zone string) bool {
	hostOlaresID, ok := hostOlaresIDForZone(ld, zone)
	if !ok {
		return false
	}
	return strings.HasSuffix("."+ld, "."+olaresID+"."+zone) ||
		sameOlaresClusterSimulated(olaresID, hostOlaresID)
}

func checkOlaresCustomDomain(ld, olaresID, loginDom, loginUser string, publicZones []string) error {
	pubZone, underPublicSuffix := olaresPublicZoneForHost(ld, publicZones)

	if underPublicSuffix {
		if loginDom == pubZone && olaresCustomHostAllowed(ld, olaresID, pubZone) {
			return nil
		}
		return fmt.Errorf("custom domain [%s] is not allowed for user [%s]", ld, loginUser)
	}

	if !slices.Contains(publicZones, loginDom) && strings.HasSuffix(ld, "."+loginDom) && !olaresCustomHostAllowed(ld, olaresID, loginDom) {
		return fmt.Errorf("custom domain [%s] is not allowed for user [%s]", ld, loginUser)
	}
	return nil
}
