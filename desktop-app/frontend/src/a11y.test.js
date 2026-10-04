import { describe, it, expect } from 'vitest';
import { detectOSTheme } from './a11y.js';

function fakeWin(prefs, extra = {}) {
  return { matchMedia: (q) => ({ matches: !!prefs[q] }), ...extra };
}

const LIGHT = '(prefers-color-scheme: light)';
const MORE = '(prefers-contrast: more)';

describe('detectOSTheme', () => {
  it('follows the OS light preference on desktop', () => {
    expect(detectOSTheme(fakeWin({ [LIGHT]: true }))).toBe('light');
    expect(detectOSTheme(fakeWin({}))).toBe('dark');
  });

  it('keeps the phone apps on the dark brand look in light mode', () => {
    expect(detectOSTheme(fakeWin({ [LIGHT]: true }, { STMAndroid: {} }))).toBe('dark');
    expect(detectOSTheme(fakeWin({ [LIGHT]: true }, { STMNative: {} }))).toBe('dark');
  });

  it('still honours more contrast in the phone apps', () => {
    expect(detectOSTheme(fakeWin({ [MORE]: true }, { STMNative: {} }))).toBe('contrast');
  });
});
