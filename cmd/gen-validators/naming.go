// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import "strings"

// goTypeName converts a schema name to a Go type name.
func goTypeName(schemaName string) string {
	parts := strings.Split(schemaName, "_")
	var result strings.Builder
	for _, part := range parts {
		if part != "" {
			result.WriteString(strings.ToUpper(part[:1]))
			result.WriteString(part[1:])
		}
	}
	return result.String()
}

// goModelType returns the client-native model type name.
func goModelType(schemaName string) string {
	switch schemaName {
	case "server":
		return "Server"
	case "server_template":
		return "ServerTemplate"
	case "bind":
		return "Bind"
	case "http_request_rule":
		return "HTTPRequestRule"
	case "http_response_rule":
		return "HTTPResponseRule"
	case "tcp_request_rule":
		return "TCPRequestRule"
	case "tcp_response_rule":
		return "TCPResponseRule"
	case "http_after_response_rule":
		return "HTTPAfterResponseRule"
	case "http_error_rule":
		return "HTTPErrorRule"
	case "server_switching_rule":
		return "ServerSwitchingRule"
	case "backend_switching_rule":
		return "BackendSwitchingRule"
	case "stick_rule":
		return "StickRule"
	case "acl":
		return "ACL"
	case "filter":
		return "Filter"
	case "log_target":
		return "LogTarget"
	case "http_check":
		return "HTTPCheck"
	case "tcp_check":
		return "TCPCheck"
	case "capture":
		return "Capture"
	default:
		return goTypeName(schemaName)
	}
}

// goFieldName converts a JSON field name to a Go field name.
// This mapping must match the field names in github.com/haproxytech/client-native/v6/models.
func goFieldName(jsonName string) string {
	// Handle common mappings
	fieldMappings := map[string]string{
		"name":                   "Name",
		"address":                "Address",
		"port":                   "Port",
		"id":                     "ID",
		"agent-addr":             "AgentAddr",
		"agent-check":            "AgentCheck",
		"agent-inter":            "AgentInter",
		"agent-port":             "AgentPort",
		"agent-send":             "AgentSend",
		"allow_0rtt":             "Allow0rtt",
		"alpn":                   "Alpn",
		"backup":                 "Backup",
		"check":                  "Check",
		"check-pool-conn-name":   "CheckPoolConnName",
		"check-reuse-pool":       "CheckReusePool",
		"check-send-proxy":       "CheckSendProxy",
		"check-sni":              "CheckSni",
		"check-ssl":              "CheckSsl",
		"check_alpn":             "CheckAlpn",
		"check_proto":            "CheckProto",
		"check_via_socks4":       "CheckViaSocks4",
		"ciphers":                "Ciphers",
		"ciphersuites":           "Ciphersuites",
		"client_sigalgs":         "ClientSigalgs",
		"cookie":                 "Cookie",
		"crl_file":               "CrlFile",
		"curves":                 "Curves",
		"downinter":              "Downinter",
		"error_limit":            "ErrorLimit",
		"fall":                   "Fall",
		"fastinter":              "Fastinter",
		"force_sslv3":            "ForceSslv3",
		"force_tlsv10":           "ForceTlsv10",
		"force_tlsv11":           "ForceTlsv11",
		"force_tlsv12":           "ForceTlsv12",
		"force_tlsv13":           "ForceTlsv13",
		"guid":                   "GUID",
		"guid_prefix":            "GUIDPrefix", // Note: GUIDPrefix not GuidPrefix
		"hash_key":               "HashKey",
		"health_check_address":   "HealthCheckAddress",
		"health_check_port":      "HealthCheckPort",
		"idle_ping":              "IdlePing",
		"init-addr":              "InitAddr",
		"init-state":             "InitState",
		"inter":                  "Inter",
		"log-bufsize":            "LogBufsize",
		"log_proto":              "LogProto",
		"maintenance":            "Maintenance",
		"max_reuse":              "MaxReuse",
		"maxconn":                "Maxconn",
		"maxqueue":               "Maxqueue",
		"minconn":                "Minconn",
		"namespace":              "Namespace",
		"no_sslv3":               "NoSslv3",
		"no_tls_tickets":         "NoTLSTickets", // Note: TLS not Tls
		"no_tlsv10":              "NoTlsv10",
		"no_tlsv11":              "NoTlsv11",
		"no_tlsv12":              "NoTlsv12",
		"no_tlsv13":              "NoTlsv13",
		"no_verifyhost":          "NoVerifyhost",
		"npn":                    "Npn",
		"observe":                "Observe",
		"on-error":               "OnError",
		"on-marked-down":         "OnMarkedDown",
		"on-marked-up":           "OnMarkedUp",
		"pool_conn_name":         "PoolConnName",
		"pool_low_conn":          "PoolLowConn",
		"pool_max_conn":          "PoolMaxConn",
		"pool_purge_delay":       "PoolPurgeDelay",
		"proto":                  "Proto",
		"proxy-v2-options":       "ProxyV2Options",
		"redir":                  "Redir",
		"resolve-net":            "ResolveNet",
		"resolve-prefer":         "ResolvePrefer",
		"resolve_opts":           "ResolveOpts",
		"tcp_user_timeout":       "TCPUserTimeout", // Note: TCP not Tcp
		"tls_ticket_keys":        "TLSTicketKeys",  // Note: TLS not Tls
		"tls_tickets":            "TLSTickets",     // Note: TLS not Tls
		"uid":                    "UID",            // Note: UID not Uid
		"acl_file":               "ACLFile",        // Note: ACL not Acl
		"acl_keyfmt":             "ACLKeyfmt",      // Note: ACL not Acl
		"sc_id":                  "ScID",
		"sc_idx":                 "ScIdx",
		"sc_inc_id":              "ScIncID",
		"sc_int":                 "ScInt",
		"sc_expr":                "ScExpr",
		"uri-fmt":                "URIFmt",
		"uri-match":              "URIMatch",
		"uri":                    "URI",
		"uri_log_format":         "URILogFormat",
		"rst_ttl":                "RstTTL",
		"resolvers":              "Resolvers",
		"rise":                   "Rise",
		"send-proxy":             "SendProxy",
		"send-proxy-v2":          "SendProxyV2",
		"send_proxy_v2_ssl":      "SendProxyV2Ssl",
		"send_proxy_v2_ssl_cn":   "SendProxyV2SslCn",
		"set-proxy-v2-tlv-fmt":   "SetProxyV2TlvFmt",
		"shard":                  "Shard",
		"sigalgs":                "Sigalgs",
		"slowstart":              "Slowstart",
		"sni":                    "Sni",
		"socks4":                 "Socks4",
		"source":                 "Source",
		"ssl":                    "Ssl",
		"ssl_cafile":             "SslCafile",
		"ssl_certificate":        "SslCertificate",
		"ssl_max_ver":            "SslMaxVer",
		"ssl_min_ver":            "SslMinVer",
		"ssl_reuse":              "SslReuse",
		"sslv3":                  "Sslv3",
		"stick":                  "Stick",
		"strict-maxconn":         "StrictMaxconn",
		"tcp_ut":                 "TCPUt",
		"tfo":                    "Tfo",
		"tlsv10":                 "Tlsv10",
		"tlsv11":                 "Tlsv11",
		"tlsv12":                 "Tlsv12",
		"tlsv13":                 "Tlsv13",
		"track":                  "Track",
		"verify":                 "Verify",
		"verifyhost":             "Verifyhost",
		"weight":                 "Weight",
		"ws":                     "Ws",
		"acl_name":               "ACLName",
		"criterion":              "Criterion",
		"value":                  "Value",
		"prefix":                 "Prefix",
		"num_or_range":           "NumOrRange",
		"fqdn":                   "Fqdn",
		"target_server":          "TargetServer",
		"cond":                   "Cond",
		"cond_test":              "CondTest",
		"type":                   "Type",
		"index":                  "Index",
		"auth_realm":             "AuthRealm",
		"bandwidth_limit_limit":  "BandwidthLimitLimit",
		"bandwidth_limit_name":   "BandwidthLimitName",
		"bandwidth_limit_period": "BandwidthLimitPeriod",
		"cache_name":             "CacheName",
		"capture_id":             "CaptureID",
		"capture_len":            "CaptureLen",
		"capture_sample":         "CaptureSample",
		"deny_status":            "DenyStatus",
		"expr":                   "Expr",
		"hdr_format":             "HdrFormat",
		"hdr_match":              "HdrMatch",
		"hdr_name":               "HdrName",
		"hint_format":            "HintFormat",
		"hint_name":              "HintName",
		"log_level":              "LogLevel",
		"lua_action":             "LuaAction",
		"lua_params":             "LuaParams",
		"map_file":               "MapFile",
		"map_keyfmt":             "MapKeyfmt",
		"map_valuefmt":           "MapValuefmt",
		"mark_value":             "MarkValue",
		"method_fmt":             "MethodFmt",
		"nice_value":             "NiceValue",
		"normalizer":             "Normalizer",
		"normalizer_full":        "NormalizerFull",
		"normalizer_strict":      "NormalizerStrict",
		"path_fmt":               "PathFmt",
		"path_match":             "PathMatch",
		"protocol":               "Protocol",
		"query_fmt":              "QueryFmt",
		"redir_code":             "RedirCode",
		"redir_option":           "RedirOption",
		"redir_type":             "RedirType",
		"redir_value":            "RedirValue",
		"resolvers_id":           "ResolversID",
		"return_content":         "ReturnContent",
		"return_content_format":  "ReturnContentFormat",
		"return_content_type":    "ReturnContentType",
		"return_hdrs":            "ReturnHdrs",
		"return_status_code":     "ReturnStatusCode",
		"service_name":           "ServiceName",
		"spoe_engine":            "SpoeEngine",
		"spoe_group":             "SpoeGroup",
		"status":                 "Status",
		"status_reason":          "StatusReason",
		"strict_mode":            "StrictMode",
		"timeout":                "Timeout",
		"timeout_type":           "TimeoutType",
		"tos_value":              "TosValue",
		"track_sc0_key":          "TrackSc0Key",
		"track_sc1_key":          "TrackSc1Key",
		"track_sc2_key":          "TrackSc2Key",
		"track_sc_key":           "TrackScKey",
		"track_sc_stick_counter": "TrackScStickCounter",
		"track_sc_table":         "TrackScTable",
		"var_expr":               "VarExpr",
		"var_format":             "VarFormat",
		"var_name":               "VarName",
		"var_scope":              "VarScope",
		"wait_at_least":          "WaitAtLeast",
		"wait_time":              "WaitTime",
	}

	if mapped, ok := fieldMappings[jsonName]; ok {
		return mapped
	}

	// Default: convert to PascalCase
	parts := strings.FieldsFunc(jsonName, func(r rune) bool {
		return r == '-' || r == '_'
	})
	var result strings.Builder
	for _, part := range parts {
		if part != "" {
			result.WriteString(strings.ToUpper(part[:1]))
			result.WriteString(part[1:])
		}
	}
	return result.String()
}
