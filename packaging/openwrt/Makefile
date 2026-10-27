include $(TOPDIR)/rules.mk

PKG_NAME:=quicssh-punch
PKG_VERSION:=1.0.3
PKG_RELEASE:=1

PKG_SOURCE:=$(PKG_NAME)-$(PKG_VERSION).tar.gz
PKG_SOURCE_URL:=https://codeload.github.com/shafiev/quicssh-punch/tar.gz/v$(PKG_VERSION)?
PKG_HASH:=30a1fdfce1c2242b6154045aa5a07a1f85cc744c2a7cc76a8d2350aa67a75c6f

PKG_MAINTAINER:=Nail Shafiev <n.shafiev@small.kz>
PKG_LICENSE:=AGPL-3.0-only
PKG_LICENSE_FILES:=LICENSE

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_BUILD_FLAGS:=no-mips16

GO_PKG:=quicssh-punch
GO_PKG_BUILD_PKG:=quicssh-punch

include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

define Package/quicssh-punch
  SECTION:=net
  CATEGORY:=Network
  TITLE:=UDP hole-punch relay for quicssh
  URL:=https://github.com/shafiev/quicssh-punch
  DEPENDS:=$(GO_ARCH_DEPENDS)
endef

define Package/quicssh-punch/description
  Punches a fixed-port UDP hole through NAT (STUN + ntfy.sh rendezvous)
  and relays QUIC through it, so quicssh works without a public IPv4
  address.
endef

define Package/quicssh-punch/conffiles
/etc/config/quicssh-punch
endef

define Package/quicssh-punch/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(GO_PKG_BUILD_BIN_DIR)/quicssh-punch $(1)/usr/bin/
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/quicssh-punch.init $(1)/etc/init.d/quicssh-punch
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/quicssh-punch.config $(1)/etc/config/quicssh-punch
endef

$(eval $(call BuildPackage,quicssh-punch))
