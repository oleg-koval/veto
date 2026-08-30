"""Native Hermes registration surface for Veto."""

from .runtime import VetoRuntime


def register(ctx):
    runtime = VetoRuntime()
    for name, description, schema, handler in runtime.tools():
        ctx.register_tool(
            name=name,
            toolset="veto",
            schema=schema,
            handler=handler,
            description=description,
            emoji="🛡️",
        )
    for name, description, args_hint, handler in runtime.commands():
        ctx.register_command(
            name=name,
            handler=handler,
            description=description,
            args_hint=args_hint,
        )
