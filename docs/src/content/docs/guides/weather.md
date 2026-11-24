---
title: Weather
description: Forecasts from several models, and airports' readings, for the temperature markets.
---

Two nodes, under **Weather**, bring the weather into a workflow, for Polymarket's
temperature markets above all: **Weather forecast**, what several weather models expect,
and **Airport reading**, what the station a market resolves by reports now.

## Cities

Both nodes take a **City**: one of the cities Polymarket lists temperature markets for,
such as NYC, London or Seoul (Incheon). A city brings:

- the station its markets resolve by, such as LaGuardia (`KLGA`) for NYC;
- that station's coordinates, for the forecast;
- its time zone, whose dates the markets are for;
- its unit, °F for the US cities and °C elsewhere.

The list comes from the markets' own rules. A few cities resolve by something other than
their airport's reports, and their items say so under `note`: Hong Kong by the Hong Kong
Observatory, Jinan and Zhengzhou by Weather Underground in whole degrees.

Without a city, the forecast takes a **Latitude** and **Longitude**, and the reading a
**Station**, an airport's ICAO code. **Unit** is `city` to begin with: the city's, or °C
by hand.

A [Schedule](/docs/guides/concepts/#schedules) running at times of day takes a city too,
under **City's time zone**: `06:30` in Austin is 06:30 there all year.

## Weather forecast

The forecast goes on each item, under **Forecast field**, `forecast` unless you name
another. **Models** are those side by side: GFS, ECMWF and ICON to begin with, and GEM,
JMA, UKMO, Météo-France, MET Norway, KNMI, DMI, CMA, ECMWF's AIFS and 9 km HRES, NBM and
HRRR for the US, and `best`, Open-Meteo's own blend.

| Field | What it holds |
|---|---|
| `forecast.daily` | A day each, today first, **Days** of them, 3 unless you set up to 16. |
| `forecast.daily[0].high.gfs` | A model's high that day; `null` where it has none. |
| `forecast.daily[0].highMean`, `lowMean` | The models' highs and lows, averaged. |
| `forecast.hourly` | An hour each, with each model's `temp` and their `mean`, when **Hourly** is on. |
| `forecast.zone`, `unit` | The time zone the days are in, and `F` or `C`. |

So the models' mean high for today is `{{ forecast.daily[0].highMean }}`, ready to compare
with a market's bucket. Forecasts come from [Open-Meteo](https://open-meteo.com). Every
workflow asking for the same place and models within **Forecasts kept for** (10 minutes to
begin with, an admin's setting) shares one answer.

Open-Meteo's free API is for non-commercial use. An instance used commercially needs a
subscription: an admin puts its key under **Config → Weather → Open-Meteo key**, and
forecasts come from the customer API from then on.

## Airport reading

The station's latest report, its METAR from [aviationweather.gov](https://aviationweather.gov),
goes on each item under **Reading field**, `airport` unless you name another: `temp` in the
unit and `tempC`, `dewpoint`, `wind`, `observedAt` and the report as sent under `raw`.

`airport.today` is the local day so far, counted as the markets count it: routine reports
(METAR) only, special ones (SPECI) left out, in tenths of a degree Celsius where the report
gives them, through the city's day, or the **Time zone** you give a station by hand.

| Field | What it holds |
|---|---|
| `airport.today.high`, `low` | The highest and lowest so far, in the unit. |
| `airport.today.highC`, `lowC` | The same in °C. |
| `airport.today.reports` | How many routine reports it counted. |
| `airport.today.date` | The local day, like `2026-11-24`. |

## When they fail

Both nodes only read, so a failure is tried again 3 more times to begin with: Open-Meteo or
aviationweather.gov busy, say. A place that is none, or a station that is not one, fails
at once. A station with no report in 30 hours fails its items, saying so.
