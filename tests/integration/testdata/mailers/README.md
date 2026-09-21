# Mailer fixtures

The integration tests use these unmodified upstream scripts:

| HAProxy versions | Fixture | Upstream source | SHA-256 |
| --- | --- | --- | --- |
| 3.1–3.2 | `mailers-legacy.lua` | [HAProxy 3.0.0](https://github.com/haproxy/haproxy/blob/v3.0.0/examples/lua/mailers.lua) | `401e0d82719912ff4d4bd0ecf2e9a83bfad6c076b24554dde5b7405e3a52dc6b` |
| 3.3 and later | `mailers.lua` | [HAProxy 3.3.0](https://github.com/haproxy/haproxy/blob/v3.3.0/examples/lua/mailers.lua) | `62add896a12d11fb79e61dbe3dee793a0f48ac93476b121ca9fa9198e799131d` |

HAProxy 3.0 uses native mailers. Later versions load the matching script and reference the mailer section so HAProxy retains the section under test. HAProxy 3.3 and later require [Lua for email alerts](https://www.haproxy.com/documentation/haproxy-configuration-tutorials/alerts-and-monitoring/email-alerts/).

The upstream GPL v2 license and OpenSSL exception for these Lua files are retained in `COPYING` and `LICENSE`.
