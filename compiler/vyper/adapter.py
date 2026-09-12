"""Version adapters for authenticated official compilers; no source filesystem fallback."""
import copy


def compile_input(value):
    import vyper
    from vyper.cli.vyper_json import compile_json, exc_handler_to_dict, TRANSLATE_MAP

    version = tuple(int(part) for part in vyper.__version__.split("."))
    request = copy.deepcopy(value)
    settings = request["settings"]
    target, requested = next(iter(settings["outputSelection"].items()))
    settings["outputSelection"] = {target: [name for name in requested if name in TRANSLATE_MAP]}
    if version < (0, 4, 0):
        settings.pop("search_paths", None)
    if version < (0, 3, 10) and "optimize" in settings:
        mode = settings["optimize"]
        if isinstance(mode, str):
            if mode not in ("none", "gas"):
                raise ValueError("unsupported compiler optimization")
            settings["optimize"] = mode == "gas"
    # Every historical compile_json defaults root_folder to None. Passing no
    # root explicitly keeps missing imports inside the inline input boundary.
    if version <= (0, 4, 0):
        # Historical Standard JSON drops layout even when the compiler produces
        # it. Preserve that exact compiler output at the formatting boundary.
        from vyper.cli import vyper_json
        original = vyper_json.format_to_output_dict
        from vyper import compiler as core
        original_layout = core.OUTPUT_FORMATS.get("layout")
        if (0, 3, 1) <= version <= (0, 3, 3):
            from vyper import ast as vy_ast
            def layout_with_code(data):
                storage = original_layout(data)
                code = {}
                for node in data.vyper_module_folded.get_children(vy_ast.AnnAssign, filters={"annotation.func.id": "immutable"}):
                    typ = node._metadata["type"]
                    code[node.target.id] = {"type": str(typ), "offset": typ.position.offset, "length": ((typ.size_in_bytes + 31) // 32) * 32}
                return {"storage_layout": storage, "code_layout": code}
            core.OUTPUT_FORMATS["layout"] = layout_with_code
        def format_with_layout(data):
            result = original(data)
            for filename, raw in data.items():
                contracts = result.get("contracts", {}).get(str(filename), {})
                for artifact in contracts.values():
                    artifact["layout"] = raw.get("layout", {})
            return result
        vyper_json.format_to_output_dict = format_with_layout
        try:
            output = compile_json(request, exc_handler_to_dict)
        finally:
            vyper_json.format_to_output_dict = original
            if original_layout is not None:
                core.OUTPUT_FORMATS["layout"] = original_layout
    else:
        output = compile_json(request, exc_handler_to_dict)
    contracts = output.get("contracts", {})
    selected = contracts.get(target)
    if selected is not None:
        output["contracts"] = {target: selected}
        for artifact in selected.values():
            artifact.setdefault("layout", {})
            artifact.setdefault("metadata", {})
    return output
