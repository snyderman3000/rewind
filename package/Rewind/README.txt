REWIND — new movie releases, for OnionOS (Miyoo Mini Plus)
============================================================
Built by Claude, Anthropic's AI model, for snyderman3000.
Source, updates and bug reports: https://github.com/snyderman3000/rewind

Rewind lists movies that recently came out digitally in your country,
on a streaming service or to rent or buy (rentals are marked $), with their Rotten
Tomatoes Tomatometer, IMDb rating, Metascore, age rating, runtime, cast,
director and which services carry them. Sort and filter by score, genre,
service, rating and runtime. Each movie has a "trailer reel" of stills and
a QR code that opens the real trailer on your phone. If you run Radarr,
press X on a movie to add it, and Radarr downloads it into your library.

INSTALL
  1. Copy the "App" folder from this zip to the root of your SD card
     (merge it with the App folder that's already there).
  2. Get two free API keys:
       TMDB  https://www.themoviedb.org/settings/api   (required)
       OMDb  https://www.omdbapi.com/apikey.aspx      (for Rotten Tomatoes scores)
  3. Copy App/Rewind/config.example.json to App/Rewind/data/config.json
     and paste the keys in. (Or open Rewind once: it creates
     data/config.json for you and shows QR codes for both sign-up pages.)
  4. Turn Wi-Fi on and open Apps → Rewind. The first load takes a minute
     or so; after that the guide opens instantly from the SD card and
     refreshes itself in the background.

CONTROLS
  LIST     Up/Down move · Left/Right page · A details
           X add to Radarr · L1/R1 genre · L2/R2 streaming service
           Y sort · START menu (filters, refresh) · SELECT search · B exit
  SEARCH   arrows move · A type · B delete · X space · Y clear
           START search · SELECT cancel
  RESULTS  A details · X add to Radarr · SELECT new search · B back
  DETAILS  A trailer · X add to Radarr · Y recheck Radarr
           Up/Down scroll synopsis · L1/R1 previous/next · B back
  TRAILER  A pause/play · Left/Right still · L1/R1 previous/next
           X add to Radarr · B back
  MENU exits from anywhere.

SEARCH (SELECT)
  Type any movie's title, however old, on the on-screen keyboard. The
  results show scores, details and services like the guide does, and X
  records one to Radarr. Your last five searches are listed under the keys.

MENU (START)
  Sort by Tomatometer, newest, popular, IMDb, Metascore, title or runtime.
  Filter by genre, service or store, streaming vs. rental (WATCH ON),
  minimum Tomatometer (60/75/90%), age rating
  (G, up to PG, up to PG-13, R only), runtime, and hide unscored movies.
  Your choices are remembered. REFRESH GUIDE (press A) reloads the guide
  now; it also refreshes by itself every few hours.

SETTINGS  (App/Rewind/data/config.json)
  tmdb_api_key   TMDB v3 API key, or the longer v4 "API Read Access Token"
  omdb_api_key   OMDb key (free tier: 1,000 lookups a day; Rewind caches scores)
  region         two-letter country for services and ratings (default US)
  language       titles and synopses (default en-US)
  days           how recent a release must be (default 120)
  max_movies     how many movies to keep in the guide (default 250,
                 up to 400; more means a longer first load)
  providers      limit to services by TMDB id, e.g. [8, 9, 337]
                 (8 Netflix, 9 Prime Video, 337 Disney+, 15 Hulu,
                  1899 Max, 531 Paramount+, 386 Peacock, 350 Apple TV+)
  refresh_hours  how old the guide can get before it refreshes (default 6)

RADARR (optional)
  radarr_url              your Radarr address on the home network,
                          e.g. http://192.168.1.20:7878 (add any URL base,
                          like http://192.168.1.20:7878/radarr)
  radarr_api_key          Radarr → Settings → General → API Key
  radarr_quality_profile  profile name, e.g. "HD-1080p" (blank = first one)
  radarr_root_folder      e.g. "/movies" (blank = first one)
  radarr_search           false = add without searching right away
  radarr_minimum_availability  released (default), inCinemas or announced

  With Radarr set up, the shelf marks movies you already have
  (● downloaded, ○ in Radarr, still coming), the details screen shows
  download progress, and the filters can hide or show only what you have.
  If Radarr's Connect settings notify Plex, new downloads show up in Plex
  on their own. The Miyoo has to be on the same network as Radarr.

ABOUT THE TRAILERS
  The Miyoo can't play YouTube video, so Rewind shows a slideshow of the
  film's stills in a VCR screen and a QR code: scan it with your phone to
  watch the real trailer.

ABOUT "NEW"
  Rewind takes recent films you can stream, rent or buy now: digital
  releases, films from US theaters and films that premiered in the last
  "days" days, plus streaming releases from the past year. A theatrical
  film shows up as soon as it can be rented, even before TMDB records its
  digital release date (it then shows RELEASED instead of DIGITAL). TMDB
  doesn't record when a film reached a service, so when an older film
  shows up between refreshes, or a rental moves to a service, it has most
  likely just arrived, so NEWEST puts it on top and its
  details show an ARRIVED date.

DATA
  Movie data and images: TMDB. This product uses the TMDB API but is not
  endorsed or certified by TMDB. Streaming availability: JustWatch, via TMDB.
  Scores: OMDb API. Rewind isn't affiliated with Rotten Tomatoes, IMDb or
  Metacritic.

LOGS: App/Rewind/data/rewind.log

Fonts: Share Tech Mono, VT323, Orbitron (SIL Open Font License).
CA bundle: Mozilla via curl (MPL 2.0). QR encoder from rsc.io/qr (BSD).
Licenses are in the licenses folder.
