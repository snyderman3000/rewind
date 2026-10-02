REWIND — new on streaming, for OnionOS (Miyoo Mini Plus)
=========================================================
Built by Claude, Anthropic's AI model, for snyderman3000.
Source, updates and bug reports: https://github.com/snyderman3000/rewind

Rewind lists movies that recently came out digitally and are on a
subscription streaming service in your country now, with their Rotten
Tomatoes Tomatometer, IMDb rating, Metascore, age rating, runtime, cast,
director and which services carry them. Sort and filter by score, genre,
service, rating and runtime. Each movie has a "trailer reel" of stills and
a QR code that opens the real trailer on your phone.

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
           L1/R1 genre · L2/R2 streaming service · Y sort
           X or START filters · SELECT refresh now · B exit
  DETAILS  A trailer · Up/Down scroll synopsis · L1/R1 previous/next · B back
  TRAILER  A pause/play · Left/Right still · L1/R1 previous/next · B back
  MENU exits from anywhere.

FILTERS (X)
  Sort by Tomatometer, newest, popular, IMDb, Metascore, title or runtime.
  Filter by genre, service, minimum Tomatometer (60/75/90%), age rating
  (G, up to PG, up to PG-13, R only), runtime, and hide unscored movies.
  Your choices are remembered.

SETTINGS  (App/Rewind/data/config.json)
  tmdb_api_key   TMDB v3 API key, or the longer v4 "API Read Access Token"
  omdb_api_key   OMDb key (free tier: 1,000 lookups a day; Rewind caches scores)
  region         two-letter country for services and ratings (default US)
  language       titles and synopses (default en-US)
  days           how recent a digital release must be (default 120)
  max_movies     how many movies to keep in the guide (default 120)
  providers      limit to services by TMDB id, e.g. [8, 9, 337]
                 (8 Netflix, 9 Prime Video, 337 Disney+, 15 Hulu,
                  1899 Max, 531 Paramount+, 386 Peacock, 350 Apple TV+)
  refresh_hours  how old the guide can get before it refreshes (default 6)

ABOUT THE TRAILERS
  The Miyoo can't play YouTube video, so Rewind shows a slideshow of the
  film's stills in a VCR screen and a QR code: scan it with your phone to
  watch the real trailer.

ABOUT "NEW ON STREAMING"
  TMDB doesn't record the day a film was added to a service, so Rewind
  uses films whose digital release was in the last "days" days and that a
  subscription service carries now. Older films newly added to a service
  won't appear.

DATA
  Movie data and images: TMDB. This product uses the TMDB API but is not
  endorsed or certified by TMDB. Streaming availability: JustWatch, via TMDB.
  Scores: OMDb API. Rewind isn't affiliated with Rotten Tomatoes, IMDb or
  Metacritic.

LOGS: App/Rewind/data/rewind.log

Fonts: Share Tech Mono, VT323, Orbitron (SIL Open Font License).
CA bundle: Mozilla via curl (MPL 2.0). QR encoder from rsc.io/qr (BSD).
Licenses are in the licenses folder.
