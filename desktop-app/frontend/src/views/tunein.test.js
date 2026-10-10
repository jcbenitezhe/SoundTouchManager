import { describe, it, expect } from 'vitest';
import { looksLikeTuneInLink, stationFromInfo, formatDuration, guideIdOf, tuneInFavorites } from './tunein.js';

describe('looksLikeTuneInLink', () => {
  it('spots shared links and bare ids', () => {
    for (const s of [
      'https://tunein.com/radio/Apple-Music-Club-s345726/',
      'Listen on TuneIn http://tun.in/sfK2i',
      'tunein.com/radio/x-s1/',
      's345726',
      'T569445012',
    ]) expect(looksLikeTuneInLink(s), s).toBe(true);
  });
  it('leaves ordinary searches alone', () => {
    for (const s of ['jazz', 'apple music club', 'https://example.com/tunein.com/', 'g59', ''])
      expect(looksLikeTuneInLink(s), s).toBe(false);
  });
});

describe('stationFromInfo', () => {
  it('keys the station by its TuneIn reference, never a stream URL', () => {
    const s = stationFromInfo({ ref: 'tunein:s345726', name: 'Apple Music Club', codec: 'AAC', bitrate: 64, image: 'https://cdn/x.png' });
    expect(s).toMatchObject({
      stationuuid: 'tunein:s345726', url: 'tunein:s345726', url_resolved: 'tunein:s345726',
      name: 'Apple Music Club', codec: 'AAC', bitrate: 64, favicon: 'https://cdn/x.png',
    });
    expect(JSON.stringify(s)).not.toMatch(/accessKey|m3u8/);
  });
});

describe('tuneInFavorites', () => {
  it('keeps only TuneIn stations and reads the id after the prefix', () => {
    const list = tuneInFavorites([
      { stationuuid: 'tunein:s345726', name: 'Apple Music Club' },
      { stationuuid: 'rb-1', name: 'A radio-browser station' },
      { url: 'http://example.com/stream' },
    ]);
    expect(list.map(s => s.name)).toEqual(['Apple Music Club']);
    expect(guideIdOf(list[0])).toBe('s345726');
    expect(guideIdOf({ stationuuid: 'rb-1' })).toBe('');
  });
});

describe('formatDuration', () => {
  it('formats episode lengths', () => {
    expect(formatDuration(412)).toBe('6:52');
    expect(formatDuration(3725)).toBe('1:02:05');
    expect(formatDuration(0)).toBe('');
    expect(formatDuration('x')).toBe('');
  });
});
