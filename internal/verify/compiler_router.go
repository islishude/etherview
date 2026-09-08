package verify

import (
	"context"
	"errors"

	"github.com/islishude/etherview/internal/geascompiler"
)

type CompilerFamily string

const (
	CompilerFamilySolcJS CompilerFamily = "solcjs"
	CompilerFamilyGeas   CompilerFamily = "geas"
	CompilerFamilyVyper  CompilerFamily = "vyper"
)

type CompilerAvailability struct {
	SolcJS bool
	Geas   bool
	Vyper  bool
}

func (availability CompilerAvailability) Available(language Language) bool {
	switch language {
	case LanguageSolidity, LanguageYul:
		return availability.SolcJS
	case LanguageVyper:
		return availability.Vyper
	case LanguageGeas:
		return availability.Geas
	default:
		return false
	}
}

type CompilerRouter struct {
	SolcJS *SolcJSCompiler
	Geas   *GeasCompiler
	Vyper  *VyperCompiler
}

func NewCompilerRouter(solcJS *SolcJSCompiler, geas *GeasCompiler, vyper *VyperCompiler) (*CompilerRouter, error) {
	if solcJS == nil || geas == nil || vyper == nil {
		return nil, errors.New("verification compiler router requires solc-js, Geas and Vyper")
	}
	return &CompilerRouter{SolcJS: solcJS, Geas: geas, Vyper: vyper}, nil
}

func (router *CompilerRouter) ValidateRuntime(ctx context.Context) error {
	if router == nil || router.SolcJS == nil || router.Geas == nil || router.Vyper == nil {
		return errors.New("verification compiler router is incomplete")
	}
	if err := router.SolcJS.ValidateRuntime(ctx); err != nil {
		return err
	}
	if err := router.Geas.ValidateRuntime(ctx); err != nil {
		return err
	}
	return router.Vyper.ValidateRuntime(ctx)
}

func (router *CompilerRouter) Availability(ctx context.Context) CompilerAvailability {
	if router == nil {
		return CompilerAvailability{}
	}
	return CompilerAvailability{
		Vyper:  router.Vyper != nil && router.Vyper.CompilerAvailable(ctx),
		SolcJS: router.SolcJS != nil && router.SolcJS.CompilerAvailable(ctx),
		Geas:   router.Geas != nil && router.Geas.CompilerAvailable(ctx),
	}
}

func (router *CompilerRouter) CompilerAvailable(ctx context.Context) bool {
	availability := router.Availability(ctx)
	return availability.SolcJS || availability.Geas || availability.Vyper
}

func (router *CompilerRouter) Ready() bool {
	return router != nil && router.SolcJS != nil && router.SolcJS.Ready() &&
		router.Geas != nil && router.Geas.Ready() && router.Vyper != nil && router.Vyper.Ready()
}

func (router *CompilerRouter) Resolve(
	ctx context.Context,
	language Language,
	version string,
) (CompilerProvenance, error) {
	switch language {
	case LanguageSolidity, LanguageYul:
		return router.SolcJS.Resolve(ctx, language, version)
	case LanguageVyper:
		return router.Vyper.Resolve(ctx, language, version)
	case LanguageGeas:
		return router.Geas.Resolve(ctx, language, version)
	default:
		return CompilerProvenance{}, ErrCompilerVersionUnavailable
	}
}

func (router *CompilerRouter) Provenance(language Language, version string) (CompilerProvenance, error) {
	switch language {
	case LanguageSolidity, LanguageYul:
		return router.SolcJS.Provenance(language, version)
	case LanguageVyper:
		return router.Vyper.Provenance(language, version)
	case LanguageGeas:
		return router.Geas.Provenance(language, version)
	default:
		return CompilerProvenance{}, ErrCompilerVersionUnavailable
	}
}

func (router *CompilerRouter) Compile(
	ctx context.Context,
	language Language,
	version string,
	input []byte,
) ([]byte, error) {
	switch language {
	case LanguageSolidity, LanguageYul:
		return router.SolcJS.Compile(ctx, language, version, input)
	case LanguageVyper:
		return router.Vyper.Compile(ctx, language, version, input)
	case LanguageGeas:
		return router.Geas.Compile(ctx, language, version, input)
	default:
		return nil, ErrCompilerVersionUnavailable
	}
}

func (router *CompilerRouter) CompilePinned(
	ctx context.Context,
	language Language,
	version string,
	provenance CompilerProvenance,
	input []byte,
) ([]byte, error) {
	switch language {
	case LanguageSolidity, LanguageYul:
		return router.SolcJS.CompilePinned(ctx, language, version, provenance, input)
	case LanguageVyper:
		return router.Vyper.CompilePinned(ctx, language, version, provenance, input)
	case LanguageGeas:
		return router.Geas.CompilePinned(ctx, language, version, provenance, input)
	default:
		return nil, ErrCompilerVersionUnavailable
	}
}

func (router *CompilerRouter) CompilePairPinned(
	ctx context.Context,
	language Language,
	version string,
	provenance CompilerProvenance,
	first, second []byte,
) ([]byte, []byte, error) {
	if language != LanguageSolidity && language != LanguageYul && language != LanguageVyper {
		return nil, nil, ErrCompilerVersionUnavailable
	}
	firstOutput, err := router.CompilePinned(ctx, language, version, provenance, first)
	if err != nil {
		return nil, nil, err
	}
	secondOutput, err := router.CompilePinned(ctx, language, version, provenance, second)
	if err != nil {
		return nil, nil, err
	}
	return firstOutput, secondOutput, nil
}

func (router *CompilerRouter) CompileGeasEntrypointPinned(
	ctx context.Context,
	version string,
	provenance CompilerProvenance,
	sources map[string]string,
	entrypoint string,
) (geascompiler.Response, error) {
	if router == nil || router.Geas == nil {
		return geascompiler.Response{}, ErrCompilerVersionUnavailable
	}
	return router.Geas.CompileGeasEntrypointPinned(ctx, version, provenance, sources, entrypoint)
}
