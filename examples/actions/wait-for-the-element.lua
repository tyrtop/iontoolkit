return function(ctx)
  local name = ctx.state.element_name or "the element"
  ctx.status("watching " .. name)

  local wait = ctx.budget("2m")
  while wait:tick("10s") do
    local ok, _, info = pcall(ctx.run, "element.inspect", { id = ctx.state.element_id })
    for _, ch in ipairs(ok and info.status and info.status.channels or {}) do
      if ch.name == "config_and_events" and ch.connected then
        ctx.status(name .. " is back after " .. wait:spent())
        return
      end
    end
  end
  ctx.fail(name .. " has not come back in " .. wait:spent())
end
