-- Poll until the element answers, or until the budget runs out. The sleeping
-- is iontk's: wait:tick("10s") returns false once the budget is spent.

return function(ctx)
  local name = ctx.state.element_name or "the element"
  ctx.status("watching " .. name)

  local wait = ctx.budget("2m")
  while wait:tick("10s") do
    local st = ctx.try("/sdwan/v2.6/api/elements/{element_id}/status")
    if st and st.config_and_events_connected then
      ctx.status(name .. " is back after " .. wait:spent())
      return
    end
  end
  ctx.fail(name .. " has not come back in " .. wait:spent())
end
