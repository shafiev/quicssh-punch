include $(TOPDIR)/rules.mk

PKG_NAME:=luci-app-quicssh-punch
PKG_VERSION:=1.0.2
PKG_RELEASE:=1

PKG_MAINTAINER:=Nail Shafiev <n.shafiev@small.kz>
PKG_LICENSE:=AGPL-3.0-only

LUCI_TITLE:=LuCI support for quicssh-punch
LUCI_DEPENDS:=+luci-base +quicssh-punch
LUCI_PKGARCH:=all

include $(TOPDIR)/feeds/luci/luci.mk

# call BuildPackage
$(eval $(call BuildPackage,$(PKG_NAME)))
