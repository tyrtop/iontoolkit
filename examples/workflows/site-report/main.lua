return function(ctx)
  local _, sites = ctx.run("endpoint.sites", {})
  local site = ctx.lookup(sites.items, "name", ctx.input.site)
  if not site then ctx.fail("no site is called " .. ctx.input.site) end
  local _, els = ctx.run("endpoint.elements", {})
  local ids = {}
  for _, el in ipairs(els.items) do
    if el.site_id == site.id then
      ctx.status(el.name .. "  " .. el.model_name .. "  " .. el.software_version)
      ids[#ids + 1] = el.id
    end
  end
  local _, res = ctx.run("batch.run", { elements = table.concat(ids, ","), cmd = "dump spoke-ha status" })
  for _, r in ipairs(res.Results or {}) do
    if r.error then
      ctx.status((r.name or r.element_id) .. "  failed: " .. r.error)
    else
      local active = r.commands[1].output:match("Active%s*:%s*(%a+)")
      ctx.status(r.name .. "  active " .. tostring(active))
    end
  end
  return { site = site.name, routers = #ids }
end
