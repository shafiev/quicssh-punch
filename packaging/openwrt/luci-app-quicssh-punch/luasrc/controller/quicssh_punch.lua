module("luci.controller.quicssh_punch", package.seeall)

function index()
	entry({"admin", "services", "quicssh_punch"}, cbi("quicssh_punch"), _("quicssh-punch"), 60).dependent = true
end
