# Citrine's Workers

`cloudflare/` is [the shield](#the-shield), a Worker that refuses hosting
networks, and [the WAF rules](#the-waf-rules) that run before it, which setup's
`SHIELD` turns off.

**Every server deploys them to its own zone**: `citrine-cloudflare` compares the
image's `cloudflare/` and `CF_ZONE_ID` against what it last deployed, and
pushes the script, its two routes and the WAF rules through Cloudflare's API
when they differ — at boot, daily, and after a change to the secrets. An image
update is how a change to a Worker reaches every site. CI holds no Cloudflare
credential, and `CF_WORKERS_TOKEN` is written to no env file.

- **Each Worker is one file in `src/`, deployed as a module with no imports**, and
  the deploy takes `compatibilityDate` out of `cloudflare.config.ts` with `sed`. An
  import or a date written any other way breaks the server's deploy, not `cf deploy`.
- **"Already deployed" is the server's own record**, so a hand deploy of a
  different version stays until the image's copy changes. Delete
  `/var/lib/citrine/shield-deployed` to put the image's back.

## The shield

- **`SHIELD=no` stops deploying, not what is deployed**: the Worker, its routes
  and the `citrine-` rules stay on the zone, still blocking, while the Allowed tab
  goes with its tail. Deleting them in Cloudflare and turning it back on
  redeploys nothing until both of its records are deleted too.
- **The `_astro/*` routes that keep assets out of it are Worker-less routes
  that exist only in the dashboard**, one per zone; the deploy leaves any route
  whose pattern isn't a Worker's own alone.
- **It logs one `msg: "visit"` line per request it lets through**, which is the
  Allowed tab's whole source: changing the line's shape is changing the
  dashboard's reader in `probe_visits.go`. The query string stays out of it.
- The zone's WAF custom rules run first, so a request they block or challenge
  never reaches it. What the dashboard makes of its stops is in
  [dashboard.md](dashboard.md#cloudflare).

By hand, to try a change to the shield before it ships — from its own folder:

```sh
CF_ACCOUNT_ID=<account> ZONES=example.com,example.org npx cf deploy
```

## The WAF rules

`waf/*.rule` deploy with the shield, as the zone's custom rules. A rule's first line is
its name, which is what the dashboard's Blocked tab shows.

- **They stay in the WAF, not the Worker, on purpose.** Below Enterprise a Worker
  can't see `cf.client.bot`, and Free's 100,000 Worker requests a day are the
  account's: a WAF block costs nothing, and a scanner flood through the Worker
  would spend them.
- **A deploy adds or updates the image's rules by `ref`**, `citrine-` and the
  file's name, and keeps every other rule by id. Free allows five custom rules in
  all.
- **As few rules as fit, on purpose**: those five are shared with the owner's own,
  so a new test joins an existing rule rather than making one.
- **There is no rule for scanners' paths and methods on purpose**: one never
  matched on the owner's zone, because a scanner sends no browser headers and
  `scripts` stops it first.
- **Free has no `matches`**, so every test is `contains`, `starts_with` or `in`.
- **`@CHROME@`, `@FIREFOX@`, `@SAFARI@` and `@IPHONE@` are filled at deploy too**,
  from Google's version history, Mozilla's product details and Apple's developer
  releases feed, so a release reaches the rule within a day with no image update.
  They are the only versions let through, so a Chromium browser that lags Chrome is blocked. ESR's User-Agent is regular
  Firefox's, so letting ESR through lets a stale Firefox of that version through too.
- **Safari's version is iOS's**, read off the feed's `iOS` titles, because Safari
  ships inside the OS and rarely has an item of its own. Apple's `gdmf` API is
  signed by Apple's own root, which no stock trust store has. The jump from 18
  to 26 is Apple's renumbering, not a gap to fill.
- **The Safari test is on `Mac OS X`, so it covers iPhone and iPad too**: since
  Safari 26 the `iPhone OS` version is frozen at `18_6`, so `Version/` is the
  only way to age out an old iPhone, and `@IPHONE@` always keeps `18_`.
- **`@IPHONE@` still earns its place**: Chrome, Firefox and in-app browsers on
  iOS send no `Version/`, `Chrome/` or `Firefox/`, so the OS is their only age.
  It follows Safari's versions because an app that writes its own User-Agent can
  report the real one.
- **"Already deployed" is `shield-waf-deployed`**, the rules and the ranges
  together, so a hand edit stays until either changes.
