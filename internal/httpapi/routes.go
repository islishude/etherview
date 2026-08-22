package httpapi

import (
	"errors"
	"fmt"
	"slices"
)

// CapabilityRequirements identifies the API capabilities the production
// assembly explicitly enables. Tests and narrow embedded users may leave a
// capability disabled; an enabled capability always fails construction when
// any required dependency is absent.
type CapabilityRequirements struct {
	Native        bool
	Catalog       bool
	Analytics     bool
	Compatibility bool
	Events        bool
	HomeSnapshots bool
	Metadata      bool
	Proxy         bool
	Verification  bool
	Web           bool
}

type capabilityRouteModule struct {
	name     string
	validate func() error
	register func()
}

func (h *Handler) routes() error {
	modules := []capabilityRouteModule{
		{name: "operations", validate: h.validateOperationalCapability, register: h.registerOperationalRoutes},
		{name: "identity-billing", validate: h.validateIdentityBillingCapability, register: h.registerIdentityBillingRoutes},
		{name: "native", validate: h.validateNativeCapability, register: h.registerNativeRoutes},
		{name: "catalog", validate: h.validateCatalogCapability, register: h.registerCatalogRoutes},
		{name: "analytics", validate: h.validateAnalyticsCapability, register: h.registerAnalyticsRoutes},
		{name: "metadata", validate: h.validateMetadataCapability, register: h.registerMetadataRoutes},
		{name: "verification", validate: h.validateVerificationCapability, register: h.registerVerificationRoutes},
		{name: "external-surfaces", validate: h.validateExternalSurfaceCapability, register: h.registerExternalSurfaceRoutes},
	}
	for _, module := range modules {
		if err := module.validate(); err != nil {
			return fmt.Errorf("httpapi %s capability: %w", module.name, err)
		}
	}
	for _, module := range modules {
		module.register()
	}
	return nil
}

func (h *Handler) validateOperationalCapability() error { return nil }

func (h *Handler) validateIdentityBillingCapability() error {
	var errs []error
	if h.cfg.Features.UserAuth && (h.userAuth == nil || h.userAdministration == nil) {
		errs = append(errs, errors.New("enabled user authentication requires writer services"))
	}
	if h.cfg.Features.UserAPIKeys && h.userAPIKeys == nil {
		errs = append(errs, errors.New("enabled user API keys require writer services"))
	}
	if h.cfg.Features.UserAuth && h.billingReader == nil {
		errs = append(errs, errors.New("enabled user authentication requires a writer-backed billing reader"))
	}
	if h.cfg.Features.X402Billing && h.billing == nil {
		errs = append(errs, errors.New("enabled x402 billing requires a writer-backed dispatcher"))
	}
	if h.cfg.Features.X402Billing && h.requirements.Native && !h.quotaConfigured {
		errs = append(errs, errors.New("enabled x402 billing requires a quota wrapper"))
	}
	if h.cfg.Features.ENS && h.addressNames == nil {
		errs = append(errs, errors.New("enabled ENS requires a writer-backed name service"))
	}
	return errors.Join(errs...)
}

func (h *Handler) validateNativeCapability() error {
	if !h.requirements.Native {
		return nil
	}
	return missingCapabilityDependencies(map[string]bool{
		"transaction reader":   h.transactionReader == nil,
		"address activity":     h.addressActivities == nil,
		"genesis reader":       h.genesis == nil,
		"readiness projection": !h.readinessExplicit,
		"mempool reader":       h.cfg.Features.Mempool && h.mempool == nil,
	})
}

func (h *Handler) validateCatalogCapability() error {
	if !h.requirements.Catalog {
		return nil
	}
	return missingCapabilityDependencies(map[string]bool{
		"catalog reader":     h.catalog == nil,
		"address enrichment": h.addressEnrichment == nil,
		"delegation history": h.delegationHistory == nil,
	})
}

func (h *Handler) validateAnalyticsCapability() error {
	if h.requirements.Analytics && h.analytics == nil {
		return errors.New("analytics reader is required")
	}
	return nil
}

func (h *Handler) validateMetadataCapability() error {
	if !h.requirements.Metadata || !h.cfg.Features.NFTMetadata {
		return nil
	}
	return missingCapabilityDependencies(map[string]bool{
		"metadata reader": h.nftMetadataReader == nil,
		"media source":    h.nftMediaSource == nil,
		"media proxy":     h.nftMediaProxy == nil,
	})
}

func (h *Handler) validateVerificationCapability() error {
	var errs []error
	if h.requirements.Verification && h.cfg.Features.Verification {
		errs = append(errs, missingCapabilityDependencies(map[string]bool{
			"verification reader": h.verificationReader == nil,
			"compiler catalog":    h.compilerCatalog == nil,
		}))
	}
	if h.requirements.Verification && h.cfg.Security.PublicVerification {
		errs = append(errs, missingCapabilityDependencies(map[string]bool{
			"verification submitter": h.verificationSubmitter == nil,
			"target resolver":        h.verificationTargets == nil,
		}))
	}
	if h.requirements.Proxy && h.proxyReader == nil {
		errs = append(errs, errors.New("proxy reader is required"))
	}
	return errors.Join(errs...)
}

func (h *Handler) validateExternalSurfaceCapability() error {
	return missingCapabilityDependencies(map[string]bool{
		"Etherscan compatibility handler": h.requirements.Compatibility && h.etherscan == nil,
		"event broker":                    h.requirements.Events && h.events == nil,
		"home snapshot source":            h.requirements.HomeSnapshots && h.homeSnapshots == nil,
		"web handler":                     h.requirements.Web && h.web == nil,
	})
}

func missingCapabilityDependencies(dependencies map[string]bool) error {
	var errs []error
	names := make([]string, 0, len(dependencies))
	for name := range dependencies {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		missing := dependencies[name]
		if missing {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
	}
	return errors.Join(errs...)
}
