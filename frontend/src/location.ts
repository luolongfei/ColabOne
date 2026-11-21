import { colabOneLocaleCode, t } from "./i18n";
import {
  countryLabelForLocale,
  translateColabOneSubregion,
} from "./i18n/locations";
import type { InstanceLocation } from "./types";

export { translateColabOnePlaceName } from "./i18n/locations";

export function countryLabel(loc?: InstanceLocation | null): string {
  if (!loc?.country) return "";
  return countryLabelForLocale(
    loc.country,
    colabOneLocaleCode.value,
    loc.country_name,
  );
}

export function locationShort(loc?: InstanceLocation | null): string {
  return countryLabel(loc);
}

export function locationFull(loc?: InstanceLocation | null): string {
  const country = countryLabel(loc);
  if (!country) return "";
  const rawSub = (loc?.region || "").trim();
  const sub = translateColabOneSubregion(rawSub, colabOneLocaleCode.value);
  if (!sub || sub === country) return country;
  return `${country} · ${sub}`;
}

export function locationTooltip(loc?: InstanceLocation | null): string {
  const full = locationFull(loc);
  if (!full) return "";
  return loc?.zone
    ? t("location.tooltipWithZone", { full, zone: loc.zone })
    : t("location.tooltipNoZone", { full });
}
