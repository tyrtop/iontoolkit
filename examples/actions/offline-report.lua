return function(ctx)
  local el = ctx.state.element
  if not el then
    ctx.fail("no element under the cursor")
  end

  local st = ctx.get("/sdwan/v2.6/api/elements/{element_id}/status")
  local lines = {
    "element: " .. (el.name or el.id),
    "model:   " .. (el.model_name or "?") .. " " .. (el.software_version or ""),
    "serial:  " .. (el.serial_number or "-"),
  }

  if st.last_rebooted_time then
    lines[#lines + 1] = "rebooted: " .. ctx.utc(st.last_rebooted_time) ..
                        " (" .. ctx.ago(st.last_rebooted_time) .. ")"
  end

  local channels, down = 0, {}
  for name, value in pairs(st) do
    if string.find(name, "_connected$") then
      channels = channels + 1
      if value ~= true then
        down[#down + 1] = string.gsub(name, "_connected$", "")
      end
    end
  end
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
