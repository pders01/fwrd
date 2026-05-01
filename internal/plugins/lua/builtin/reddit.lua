-- reddit.lua: turn /r/<subreddit> URLs into Reddit's RSS endpoint.
--
-- Reddit exposes RSS for any subreddit by appending ".rss" to the
-- listing URL. The plugin extracts the subreddit name for a friendlier
-- title; everything else falls back to the raw URL on a miss.

return {
  name = "reddit",
  priority = 50,

  can_handle = function(url)
    return string.find(url, "://www.reddit.com/r/", 1, true) ~= nil
        or string.find(url, "://reddit.com/r/", 1, true) ~= nil
  end,

  enhance = function(url)
    local trimmed = url
    if string.sub(trimmed, -1) == "/" then
      trimmed = string.sub(trimmed, 1, -2)
    end

    local subreddit = regex.match("/r/([^/]+)", url) or "unknown"

    return {
      feed_url    = trimmed .. ".rss",
      title       = "Reddit - r/" .. subreddit,
      description = "Posts from r/" .. subreddit .. " subreddit",
      metadata    = { subreddit = subreddit },
    }
  end,
}
