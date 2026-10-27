m = Map("quicssh-punch", translate("quicssh-punch"),
	translate("UDP hole-punch relay for quicssh (STUN + ntfy.sh rendezvous)."))

s = m:section(TypedSection, "quicssh-punch", translate("Settings"))
s.anonymous = true
s.addremove = false

enabled = s:option(Flag, "enabled", translate("Enabled"))
enabled.rmempty = false

port = s:option(Value, "port", translate("UDP port"))
port.datatype = "port"
port.default = "44433"

role = s:option(ListValue, "role", translate("Role"))
role:value("A", translate("Server (A)"))
role:value("B", translate("Client (B)"))
role.default = "A"

topic = s:option(Value, "topic", translate("ntfy topic"))
topic.rmempty = true

target = s:option(Value, "target", translate("Target (server mode)"))
target.placeholder = "10.144.1.1:4433"
target.rmempty = true

peer = s:option(Value, "peer", translate("Static peer (testing)"))
peer.rmempty = true

stun = s:option(Value, "stun", translate("Primary STUN server"))
stun.default = "stun.l.google.com:19302"

stun2 = s:option(Value, "stun2", translate("Secondary STUN server"))
stun2.default = "stun1.l.google.com:19302"

relay = s:option(Value, "relay", translate("Relay fallback address"))
relay.rmempty = true

secret = s:option(Value, "secret", translate("Shared secret"))
secret.password = true
secret.rmempty = true

metrics = s:option(Value, "metrics", translate("Metrics listen address"))
metrics.placeholder = "127.0.0.1:9090"
metrics.rmempty = true

return m
