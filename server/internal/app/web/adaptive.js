(() => {
  'use strict';

  class AdaptiveTierController {
    constructor(profiles, index = 0) {
      if (!Array.isArray(profiles) || profiles.length < 1) throw new Error('profiles are required');
      this.profiles = profiles;
      this.setIndex(index);
    }

    setIndex(index) {
      this.index = Math.max(0, Math.min(index, this.profiles.length - 1));
      this.badSeconds = 0;
      this.goodSeconds = 0;
    }

    sample(availableBandwidth, packetLoss) {
      const current = this.profiles[this.index];
      const bandwidthBad = availableBandwidth > 0 && current.minBandwidth > 0 && availableBandwidth < current.minBandwidth;
      if (this.index < this.profiles.length - 1 && (packetLoss > .05 || bandwidthBad)) this.badSeconds++;
      else this.badSeconds = 0;

      if (this.index > 0) {
        const higher = this.profiles[this.index - 1];
        if (packetLoss < .02 && availableBandwidth > higher.minBandwidth * 1.3) this.goodSeconds++;
        else this.goodSeconds = 0;
      } else this.goodSeconds = 0;

      if (this.badSeconds >= 5 && this.index < this.profiles.length - 1) {
        this.setIndex(this.index + 1);
        return this.index;
      }
      if (this.goodSeconds >= 20 && this.index > 0) {
        this.setIndex(this.index - 1);
        return this.index;
      }
      return null;
    }
  }

  if (typeof module !== 'undefined' && module.exports) module.exports = { AdaptiveTierController };
  if (typeof window !== 'undefined') window.AdaptiveTierController = AdaptiveTierController;
})();
