# SPDX-License-Identifier: MIT
#
# Copyright (C) 2026 gitHardie
# WrtAfterAI MCP Server - 让任意 AI Agent（Coze/WorkBuddy 等）安全访问路由器

include $(TOPDIR)/rules.mk

PKG_NAME:=wrtafterai-mcp
PKG_VERSION:=0.1.0
PKG_RELEASE:=1
PKG_MAINTAINER:=gitHardie <gitHardie@users.noreply.github.com>
PKG_LICENSE:=MIT
PKG_LICENSE_FILES:=LICENSE
PKG_CONFIG_DEPENDS:=CONFIG_PACKAGE_$(PKG_NAME)

GO_PKG:=github.com/gitHardie/wrtafterai-mcp
GO_PKG_LDFLAGS_X:=main.version=$(PKG_VERSION)

PKG_BUILD_DIR:=$(BUILD_DIR)/$(PKG_NAME)-$(PKG_VERSION)

include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

define Package/$(PKG_NAME)
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=Web Servers/Proxies
  TITLE:=WrtAfterAI MCP server (AI agent access to router)
  URL:=https://github.com/gitHardie/wrtafterai-package
  DEPENDS:=$(GO_ARCH_DEPENDS) +ca-bundle
endef

define Package/$(PKG_NAME)/description
  Model Context Protocol server running on the router itself.
  Read-only tools (M0): interfaces, WAN, wireless/connected clients,
  conntrack, traffic, DHCP leases, firewall, logs. Three-level token
  policy, audit log, LAN http + optional Cloudflare Tunnel remote access.
  One-time setup: wrtafterai-mcp setup ; tokens: wrtafterai-mcp token add/list/revoke.
endef

define Package/$(PKG_NAME)/conffiles
/etc/config/wrtafterai-mcp
endef

define Build/Prepare
	$(MKDIR) $(PKG_BUILD_DIR)
	$(CP) $(CURDIR)/*.go $(CURDIR)/go.mod $(CURDIR)/go.sum $(PKG_BUILD_DIR)/
	$(CP) $(CURDIR)/internal $(PKG_BUILD_DIR)/
	$(CP) $(CURDIR)/vendor $(PKG_BUILD_DIR)/
endef

define Package/$(PKG_NAME)/install
	$(call GoPackage/Package/Install/Bin,$(1))
	$(INSTALL_DIR) $(1)/etc/init.d $(1)/etc/config $(1)/etc/uci-defaults
	$(INSTALL_BIN) ./files/wrtafterai-mcp.init $(1)/etc/init.d/wrtafterai-mcp
	$(INSTALL_CONF) ./files/wrtafterai-mcp.config $(1)/etc/config/wrtafterai-mcp
	$(INSTALL_BIN) ./files/wrtafterai-mcp.uci-defaults $(1)/etc/uci-defaults/99-wrtafterai-mcp
endef

$(eval $(call GoBinPackage,$(PKG_NAME)))
