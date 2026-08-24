# app/other.rb — second caller in app/; ambiguous `init` with main.rb.
#
# Both app/main.rb and app/other.rb live in `app/`, so the byDir index
# records `app.init` TWICE (two distinct NodeIds, one per source
# location). The resolver drops a duplicate by directory
# (dirAmbiguous["app"]["init"] = true), so any reference to `init`
# from inside `app/` — including the search/resolve.Strict lookup —
# returns AMBIGUOUS, mirroring the bash/python "two files, same def"
# shape.

def init
  'init-other'
end