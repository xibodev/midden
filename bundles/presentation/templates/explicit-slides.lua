function Meta(meta)
  meta.pagetitle = meta.pagetitle or meta.title
  meta.title = nil
  return meta
end
