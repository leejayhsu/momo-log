- golang server rendered webapp
- persistence with sqlite
- use std lib when possible
- will be deployed in home network with docker compose
- should expose a port for external access with cloudflare tunnels
- should have a path sharing with docker host so sqlite data is not lost on container destruction
- use std lib for router, but use templ and shadcn-templ for ui components.
- use the latest version of golang and all dependencies when possible. you are allowed to update my version of golang.
- IMPORTANT: write the bare minimum for tests, this is NOT a production grade app. it's simply for personal usage.
- in general, i'm running the app on localhost:8081. if nothing is running there, just tell me. don't run your own process.

# BE
- this is a pet bathroom tracking app (basically tracking when my dog Momo pees and poos)
- the core functionality is to track bathroom trips. user will indicate if the trip included a poo or not.
- Momo always pees when she goes out, so no need to track that info
- so the relevant info tracked is: how often she goes out, and how many of those include a poo.
- the primary page is an event creation widget. simply 2 big icons, poo or no poo. when pressed, the backend will record the timestamp of the bathroom trip, and save it to sqlite
- must also support an api mode for a future home assistant widget that will record bathroom trips. primary usage will be server rendered webapp, but may also build a home assistant widget in the future that needs simple api's to call.
- i don't need auth, this is zero risk app.
- but since i don't have auth, let's put in aggresive rate limiting. Momo only goes out 3-5 times a day, so our rate limits on api hits can be 10x/day. we can also set really aggresive read limits.
- even if people hammer the api to trigger bathroome events, that's fine too, this is not a big deal to me.
- api should support creating a bathroom event, and listing bathroom events. it should take a single query param, how many days to look back (1 is current day only, so maybe the query param should be named "days).

# FE
- use https://shadcn-templ.com for html components
- html pages should be easy to use on a 16" touchscreen monitor (not a tablet, just a portable monitor)
- primary usage will be displayed on a 16" touchscreen home assistant dashboard

- must build a page which shows a chart of past bathroom trips, and whether they include poo.
