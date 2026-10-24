return function(ctx)
  local el = ctx.state.element
  if not el then
    ctx.fail("no element under the cursor")
  end

  local _, info = ctx.run("element.inspect", { id = ctx.state.element_id })
  local st = info.status or {}
  local lines = {
    "element: " .. (el.name or el.id),
    "model:   " .. (el.model_name or "?") .. " " .. (el.software_version or ""),
    "serial:  " .. (el.serial_number or "-"),
  }

  if st.last_rebooted then
    lines[#lines + 1] = "rebooted: " .. st.last_rebooted
  end

  local down = {}
  for _, ch in ipairs(st.channels or {}) do
    if not ch.connected then
      down[#down + 1] = ch.name
    end
  end
  local channels = #(st.channels or {})
  if channels > 0 then
    if #down == 0 then
      lines[#lines + 1] = "channels: all " .. channels .. " up"
    else
      lines[#lines + 1] = "channels: " .. #down .. " of " .. channels ..
                          " down -- " .. table.concat(down, ", ")
    end
  end

  local where = ctx.export(table.concat(lines, "\n"), "status-report")
  ctx.status("wrote " .. #lines .. " lines to " .. where)
end
