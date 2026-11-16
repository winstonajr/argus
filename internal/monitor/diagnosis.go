package monitor

import (
	"fmt"

	"argus/internal/checker"
)

// Diagnose evaluates DNS and IP states to pinpoint the root cause of an incident.
func Diagnose(dns *CheckSnapshot, ip *CheckSnapshot) (summary string, detail string) {
	// If both configured and both are UP
	if (dns == nil || !dns.Configured || dns.Status == StatusUP) &&
		(ip == nil || !ip.Configured || ip.Status == StatusUP) {
		return "Operacional", ""
	}

	dnsFailing := dns != nil && dns.Configured && (dns.Status == StatusDown || dns.Status == StatusDegraded)
	ipFailing := ip != nil && ip.Configured && (ip.Status == StatusDown || ip.Status == StatusDegraded)
	ipAlive := ip != nil && ip.Configured && ip.Status == StatusUP

	// 1. IP has permission problem
	if ip != nil && ip.Configured && ip.LastResult.ErrorCat == checker.CatPermission {
		return "Sem permissão ICMP", "Necessário conceder cap_net_raw no host/container"
	}

	// 2. TLS error or expiring soon
	if dns != nil && dns.Configured && dns.LastResult.ErrorCat == checker.CatTLS {
		return "Certificado TLS inválido", dns.LastResult.Error
	}
	if dns != nil && dns.Configured && dns.LastResult.HasTLS && dns.LastResult.OK && dns.Status == StatusDegraded && dns.LastResult.TLSDaysLeft < 15 {
		return fmt.Sprintf("Certificado TLS expira em %dd", dns.LastResult.TLSDaysLeft), "Renove o certificado SSL/TLS antes do vencimento"
	}
	if dns != nil && dns.Configured && dns.LastResult.ErrorCat == checker.CatUnexpectedBody {
		return "Conteúdo inesperado", dns.LastResult.Error
	}

	// 3. DNS domain resolution failure
	if dns != nil && dns.Configured && dns.LastResult.ErrorCat == checker.CatDNS {
		if ipAlive {
			return "Problema de DNS", fmt.Sprintf("Nome não resolve (%s), mas o IP responde ao ping", dns.LastResult.Error)
		}
		return "Problema de DNS", dns.LastResult.Error
	}

	// 4. Cloudflare or Edge Proxy failure
	if dns != nil && dns.Configured && dns.LastResult.ErrorCat == checker.CatProxy {
		if ipAlive {
			return "Aplicação fora (borda Cloudflare)", fmt.Sprintf("%s; VM/IP operacional", dns.LastResult.Error)
		}
		return "Aplicação fora (borda Cloudflare)", dns.LastResult.Error
	}

	// 5. HTTP 5xx Server Error
	if dns != nil && dns.Configured && dns.LastResult.ErrorCat == checker.CatHTTP5xx {
		if ipAlive {
			return "Aplicação fora", fmt.Sprintf("%s; VM operacional", dns.LastResult.Error)
		}
		return "Aplicação fora", dns.LastResult.Error
	}

	// 6. HTTP connection refused or timeout
	if dns != nil && dns.Configured && (dns.LastResult.ErrorCat == checker.CatConnect || dns.LastResult.ErrorCat == checker.CatTimeout) {
		if ipAlive {
			return "Serviço não responde", fmt.Sprintf("Serviço HTTP indisponível (%s), mas VM responde ao ping", dns.LastResult.Error)
		}
	}

	// 7. Both failing or IP unreachable
	if ipFailing && dnsFailing {
		return "VM ou rede fora", "Máquina não responde ao ping e serviço HTTP indisponível"
	}

	if ipFailing && (dns == nil || !dns.Configured) {
		if ip.LastResult.ErrorCat == checker.CatTCPRefused {
			return "Porta TCP recusada", ip.LastResult.Error
		}
		return "VM ou rede fora", fmt.Sprintf("Host inalcançável: %s", ip.LastResult.Error)
	}

	// 8. DNS UP but IP DOWN
	if (!dnsFailing && dns != nil && dns.Configured) && ipFailing {
		if ip.LastResult.StatusCode > 0 {
			return "Host interno com erro", fmt.Sprintf("Aplicação pública responde, mas host interno retornou HTTP %d", ip.LastResult.StatusCode)
		}
		if ip.LastResult.ErrorCat == checker.CatConnect || ip.LastResult.ErrorCat == checker.CatTimeout {
			return "Host interno inacessível", fmt.Sprintf("Aplicação responde via borda, mas host interno falhou: %s", ip.LastResult.Error)
		}
		return "Ping falhando", "Aplicação responde via HTTP, mas IP não responde ao ping (verifique firewall/ICMP)"
	}

	// 9. Generic degraded
	if (dns != nil && dns.Status == StatusDegraded) || (ip != nil && ip.Status == StatusDegraded) {
		return "Instabilidade / Lentidão", "Tempo de resposta elevado ou perda pontual de pacotes"
	}

	return "Sob investigação", ""
}
