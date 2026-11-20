package monitor

import (
	"argus/internal/checker"
	"argus/internal/i18n"
)

// Diagnose evaluates DNS and IP states to pinpoint the root cause of an incident.
func Diagnose(dns *CheckSnapshot, ip *CheckSnapshot) (summary string, detail string) {
	if (dns == nil || !dns.Configured || dns.Status == StatusUP) &&
		(ip == nil || !ip.Configured || ip.Status == StatusUP) {
		return i18n.Pick("Operacional", "Operational"), ""
	}

	dnsFailing := dns != nil && dns.Configured && (dns.Status == StatusDown || dns.Status == StatusDegraded)
	ipFailing := ip != nil && ip.Configured && (ip.Status == StatusDown || ip.Status == StatusDegraded)
	ipAlive := ip != nil && ip.Configured && ip.Status == StatusUP

	if ip != nil && ip.Configured && ip.LastResult.ErrorCat == checker.CatPermission {
		return i18n.Pick("Sem permissão ICMP", "Missing ICMP permission"),
			i18n.Pick("Necessário conceder cap_net_raw no host/container", "Grant cap_net_raw capability on host/container")
	}

	if dns != nil && dns.Configured && dns.LastResult.ErrorCat == checker.CatTLS {
		return i18n.Pick("Certificado TLS inválido", "Invalid TLS certificate"), dns.LastResult.Error
	}
	if dns != nil && dns.Configured && dns.LastResult.HasTLS {
		warnDays := dns.TLSWarnDays
		if warnDays <= 0 {
			warnDays = 15
		}
		if dns.LastResult.TLSDaysLeft < 0 {
			return i18n.Pick("Certificado TLS expirado", "TLS certificate expired"),
				i18n.Pick("Renove o certificado SSL/TLS imediatamente", "Renew the SSL/TLS certificate immediately")
		}
		if dns.LastResult.OK && dns.Status == StatusDegraded && dns.LastResult.TLSDaysLeft < warnDays {
			return i18n.Pickf("Certificado TLS expira em %dd", "TLS certificate expires in %dd", dns.LastResult.TLSDaysLeft),
				i18n.Pick("Renove o certificado SSL/TLS antes do vencimento", "Renew the SSL/TLS certificate before expiration")
		}
	}
	if dns != nil && dns.Configured && dns.LastResult.ErrorCat == checker.CatUnexpectedBody {
		return i18n.Pick("Conteúdo inesperado", "Unexpected response body"), dns.LastResult.Error
	}

	if dns != nil && dns.Configured && dns.LastResult.ErrorCat == checker.CatDNS {
		if ipAlive {
			return i18n.Pick("Problema de DNS", "DNS issue"),
				i18n.Pickf("Nome não resolve (%s), mas o IP responde ao ping", "Hostname failed to resolve (%s), but IP responds to ping", dns.LastResult.Error)
		}
		return i18n.Pick("Problema de DNS", "DNS issue"), dns.LastResult.Error
	}

	if dns != nil && dns.Configured && dns.LastResult.ErrorCat == checker.CatProxy {
		if ipAlive {
			return i18n.Pick("Aplicação fora (borda Cloudflare)", "Application down (Cloudflare edge)"),
				i18n.Pickf("%s; VM/IP operacional", "%s; VM/IP operational", dns.LastResult.Error)
		}
		return i18n.Pick("Aplicação fora (borda Cloudflare)", "Application down (Cloudflare edge)"), dns.LastResult.Error
	}

	if dns != nil && dns.Configured && dns.LastResult.ErrorCat == checker.CatHTTP5xx {
		if ipAlive {
			return i18n.Pick("Aplicação fora", "Application down"),
				i18n.Pickf("%s; VM operacional", "%s; VM operational", dns.LastResult.Error)
		}
		return i18n.Pick("Aplicação fora", "Application down"), dns.LastResult.Error
	}

	if dnsFailing && dns.LastResult.ErrorCat == checker.CatUnexpectedStatus {
		return i18n.Pick("Status HTTP inesperado", "Unexpected HTTP status"), dns.LastResult.Error
	}

	if dns != nil && dns.Configured && (dns.LastResult.ErrorCat == checker.CatConnect || dns.LastResult.ErrorCat == checker.CatTimeout) {
		if ipAlive {
			return i18n.Pick("Serviço não responde", "Service unresponsive"),
				i18n.Pickf("Serviço HTTP indisponível (%s), mas VM responde ao ping", "HTTP service unavailable (%s), but VM responds to ping", dns.LastResult.Error)
		}
		if dnsFailing && (ip == nil || !ip.Configured) {
			return i18n.Pick("Serviço não responde", "Service unresponsive"), dns.LastResult.Error
		}
	}

	if ipFailing && dnsFailing {
		return i18n.Pick("VM ou rede fora", "VM or network down"),
			i18n.Pick("Máquina não responde ao ping e serviço HTTP indisponível", "Host does not respond to ping and HTTP service is unavailable")
	}

	if ipFailing && (dns == nil || !dns.Configured) {
		switch ip.LastResult.ErrorCat {
		case checker.CatTCPRefused:
			return i18n.Pick("Porta TCP recusada", "TCP port refused"), ip.LastResult.Error
		case checker.CatHTTP5xx, checker.CatProxy:
			return i18n.Pick("Aplicação fora", "Application down"), ip.LastResult.Error
		case checker.CatUnexpectedStatus:
			return i18n.Pick("Status HTTP inesperado", "Unexpected HTTP status"), ip.LastResult.Error
		case checker.CatUnexpectedBody:
			return i18n.Pick("Conteúdo inesperado", "Unexpected response body"), ip.LastResult.Error
		case checker.CatTLS:
			return i18n.Pick("Certificado TLS inválido", "Invalid TLS certificate"), ip.LastResult.Error
		}
		return i18n.Pick("VM ou rede fora", "VM or network down"),
			i18n.Pickf("Host inalcançável: %s", "Host unreachable: %s", ip.LastResult.Error)
	}

	if (!dnsFailing && dns != nil && dns.Configured) && ipFailing {
		if ip.LastResult.StatusCode > 0 {
			return i18n.Pick("Host interno com erro", "Internal host error"),
				i18n.Pickf("Aplicação pública responde, mas host interno retornou HTTP %d", "Public app responds, but internal host returned HTTP %d", ip.LastResult.StatusCode)
		}
		if ip.LastResult.ErrorCat == checker.CatConnect || ip.LastResult.ErrorCat == checker.CatTimeout {
			return i18n.Pick("Host interno inacessível", "Internal host unreachable"),
				i18n.Pickf("Aplicação responde via borda, mas host interno falhou: %s", "App responds via edge, but internal host failed: %s", ip.LastResult.Error)
		}
		return i18n.Pick("Ping falhando", "Ping failing"),
			i18n.Pick("Aplicação responde via HTTP, mas IP não responde ao ping (verifique firewall/ICMP)", "Application responds over HTTP, but IP does not respond to ping (check firewall/ICMP)")
	}

	if (dns != nil && dns.Status == StatusDegraded) || (ip != nil && ip.Status == StatusDegraded) {
		return i18n.Pick("Instabilidade / Lentidão", "Degraded / High Latency"),
			i18n.Pick("Tempo de resposta elevado ou perda pontual de pacotes", "Elevated response time or intermittent packet loss")
	}

	return i18n.Pick("Sob investigação", "Under investigation"), ""
}
