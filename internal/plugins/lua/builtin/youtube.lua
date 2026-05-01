-- youtube.lua: resolve YouTube channel URLs to their RSS feed.
--
-- Direct cases (no network):
--   /channel/UC...  -> RSS via channel_id
--
-- Resolved cases (one HTTP GET):
--   /@handle, /c/<name>, /user/<name>, or any channel URL whose canonical
--   page advertises the RSS feed via <link rel="alternate"
--   type="application/rss+xml" href="...?channel_id=UC...">.
--
-- We intentionally do not parse HTML beyond the channel_id link tag — a
-- full goquery-style DOM walk is out of scope for the sandboxed runtime.
-- Plugins that want richer extraction can still use http.get + regex.

local function rss_url(channel_id)
  return "https://www.youtube.com/feeds/videos.xml?channel_id=" .. channel_id
end

local function fetch_channel_id(url)
  local res, err = http.get(url)
  if err or not res or res.status ~= 200 then
    return nil
  end
  return regex.match('channel_id=([A-Za-z0-9_%-]+)', res.body)
end

return {
  name = "youtube",
  priority = 100,

  can_handle = function(url)
    return string.find(url, "://www.youtube.com", 1, true) ~= nil
        or string.find(url, "://youtube.com", 1, true) ~= nil
        or string.find(url, "://m.youtube.com", 1, true) ~= nil
        or string.find(url, "://youtu.be", 1, true) ~= nil
  end,

  enhance = function(url)
    -- Direct channel-id form.
    local cid = regex.match("/channel/([A-Za-z0-9_%-]+)", url)
    if cid then
      return {
        feed_url    = rss_url(cid),
        title       = "YouTube Channel - " .. cid,
        description = "YouTube RSS feed",
        metadata    = { channel_id = cid },
      }
    end

    -- @handle form.
    local handle = regex.match("/@([A-Za-z0-9_%.%-]+)", url)
    if handle then
      local resolved = fetch_channel_id("https://www.youtube.com/@" .. handle)
      if resolved then
        return {
          feed_url    = rss_url(resolved),
          title       = "YouTube - @" .. handle,
          description = "YouTube RSS feed",
          metadata    = { channel_id = resolved, channel_handle = handle },
        }
      end
      return {
        feed_url    = url,
        title       = "YouTube - @" .. handle,
        description = "YouTube RSS feed",
        metadata    = { channel_handle = handle },
      }
    end

    -- /c/<name> or /user/<name> legacy forms.
    local legacy = regex.match("/(?:c|user)/([A-Za-z0-9_%.%-]+)", url)
    if legacy then
      local resolved = fetch_channel_id(url)
      if resolved then
        return {
          feed_url    = rss_url(resolved),
          title       = "YouTube - " .. legacy,
          description = "YouTube RSS feed",
          metadata    = { channel_id = resolved, channel_handle = legacy },
        }
      end
    end

    -- Last resort: try resolving from the page itself.
    local resolved = fetch_channel_id(url)
    if resolved then
      return {
        feed_url    = rss_url(resolved),
        title       = "YouTube",
        description = "YouTube RSS feed",
        metadata    = { channel_id = resolved },
      }
    end

    return {
      feed_url    = url,
      title       = "YouTube",
      description = "YouTube RSS feed",
      metadata    = {},
    }
  end,
}
